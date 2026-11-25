package postgresqlretriever

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/blackbirdworks/semaphore/retriever"
	"github.com/blackbirdworks/semaphore/utils"
	"github.com/blackbirdworks/semaphore/utils/fflog"
)

const (
	config2   = "config"
	flagName2 = "flag_name"
	flagset2  = "flagset"
)

var (
	errDatabaseConnectionPoolIs = errors.New("database connection pool is not initialized")
)

// defaultColumns returns the names of the columns when the configuration does not set them.
func defaultColumns() map[string]string {
	return map[string]string{
		flagName2: flagName2,
		flagset2:  flagset2,
		config2:   config2,
	}
}

type Retriever struct {
	Columns map[string]string
	// Pools is the registry that gives the connection pool of URI. The retrievers that must share their
	// connections (the flag sets of a relay proxy) are given the same registry; when nil, the retriever
	// creates one of its own at Init.
	Pools   *PoolRegistry
	logger  *fflog.FFLogger
	columns map[string]string
	pool    *pgxpool.Pool
	flagset *string
	URI     string
	Table   string
	status  retriever.Status
}

func (r *Retriever) Init(ctx context.Context, logger *fflog.FFLogger, flagset *string) error {
	r.status = retriever.RetrieverNotReady

	r.logger = logger
	if r.logger == nil {
		r.logger = &fflog.FFLogger{}
	}

	r.columns = r.getColumnNames()
	r.flagset = flagset

	if r.Pools == nil {
		r.Pools = NewPoolRegistry()
	}

	if r.pool == nil {
		r.logger.InfoContext(ctx, "Initializing PostgreSQL retriever")
		r.logger.DebugContext(ctx, "Using columns", "columns", r.columns)

		pool, err := r.Pools.GetPool(ctx, r.URI)
		if err != nil {
			r.status = retriever.RetrieverError

			return err
		}

		r.pool = pool
	}

	r.status = retriever.RetrieverReady

	return nil
}

// Status returns the current status of the retriever.
func (r *Retriever) Status() retriever.Status {
	if r == nil || r.status == "" {
		return retriever.RetrieverNotReady
	}

	return r.status
}

// Shutdown closes the database connection.
func (r *Retriever) Shutdown(ctx context.Context) error {
	if r.pool != nil {
		r.Pools.ReleasePool(ctx, r.URI)
		r.pool = nil
	}

	r.status = retriever.RetrieverNotReady

	return nil
}

// Retrieve fetches flag configuration from PostgreSQL.
func (r *Retriever) Retrieve(ctx context.Context) ([]byte, error) {
	if r.pool == nil {
		return nil, errDatabaseConnectionPoolIs
	}

	// Build the query using the configured table and column names
	query := r.buildQuery()

	// Build the arguments for the query
	args := []any{}
	if r.getFlagset() != "" {
		args = []any{r.getFlagset()}
	}

	r.logger.DebugContext(ctx, "Executing PostgreSQL query", slog.String("query", query), slog.Any("args", args))

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to execute query: %w", err)
	}
	defer rows.Close()

	// Map to store flag configurations with flag_name as key
	flagConfigs := make(map[string]any)

	for rows.Next() {
		var (
			flagName   string
			configData []byte
		)

		scanErr := rows.Scan(&flagName, &configData)
		if scanErr != nil {
			r.logger.ErrorContext(ctx, "Failed to scan row", "error", scanErr)

			continue
		}

		// Parse the JSON config data into a map
		var config map[string]any

		unmarshalErr := json.Unmarshal(configData, &config)
		if unmarshalErr != nil {
			r.logger.ErrorContext(ctx, "Failed to unmarshal config data", "error", unmarshalErr, "flagName", flagName)

			continue
		}

		flagConfigs[flagName] = config
	}

	// Check for any errors that occurred during row iteration
	errErr := rows.Err()
	if errErr != nil {
		return nil, fmt.Errorf("rows iteration error: %w", errErr)
	}

	// If no flags found, return empty JSON object
	if len(flagConfigs) == 0 {
		return []byte("{}"), nil
	}

	// Marshal the flag configurations to JSON
	result, err := json.Marshal(flagConfigs)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal flag configurations: %w", err)
	}

	r.logger.DebugContext(ctx, "Retrieved flags from PostgreSQL", "count", len(flagConfigs))

	return result, nil
}

// OutputFormat declares that this retriever always returns JSON-encoded data,
// so the manager can pick the JSON parser regardless of the global FileFormat.
func (r *Retriever) OutputFormat() string {
	return "json"
}

// buildQuery constructs the SQL query and arguments based on whether a flagset is specified.
// It uses pgx.Identifier to safely quote identifiers and prevent SQL injection.
func (r *Retriever) buildQuery() string {
	var query string

	flagNameCol := pgx.Identifier{r.columns[flagName2]}.Sanitize()
	configCol := pgx.Identifier{r.columns[config2]}.Sanitize()
	tableCol := pgx.Identifier{r.Table}.Sanitize()
	flagsetCol := pgx.Identifier{r.columns[flagset2]}.Sanitize()

	if r.getFlagset() != "" {
		query = fmt.Sprintf("SELECT %s, %s FROM %s WHERE %s = $1 ORDER BY %s",
			flagNameCol, configCol, tableCol, flagsetCol, flagNameCol)
	} else {
		query = fmt.Sprintf("SELECT %s, %s FROM %s ORDER BY %s",
			flagNameCol, configCol, tableCol, flagNameCol)
	}

	return query
}

// getColumnNames returns the column names to use for database queries.
// If r.Columns is provided, it merges those values with defaultColumns,
// using defaults for any missing entries.
func (r *Retriever) getColumnNames() map[string]string {
	// Start with a copy of defaultColumns
	columns := make(map[string]string)
	maps.Copy(columns, defaultColumns())

	// Override with any provided custom columns
	if r.Columns != nil {
		maps.Copy(columns, r.Columns)
	}

	return columns
}

func (r *Retriever) getFlagset() string {
	if r.flagset != nil && *r.flagset != "" && *r.flagset != utils.DefaultFlagSetName {
		return *r.flagset
	}

	return ""
}
