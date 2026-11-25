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

	"github.com/blackbirdworks/semaphore/pkgs/stringsx"
	"github.com/blackbirdworks/semaphore/retriever"
)

const (
	configKey      = "config"
	flagNameColumn = "flag_name"
	flagsetKey     = "flagset"
)

var (
	errDatabaseConnectionPoolIs = errors.New("database connection pool is not initialized")
)

// defaultColumns returns the names of the columns when the configuration does not set them.
func defaultColumns() map[string]string {
	return map[string]string{
		flagNameColumn: flagNameColumn,
		flagsetKey:     flagsetKey,
		configKey:      configKey,
	}
}

// Retriever retrieves flag configurations from PostgreSQL.
type Retriever struct {
	Columns map[string]string
	// Pools is the registry that gives the connection pool of URI. The retrievers that must share their
	// connections (the flag sets of a relay proxy) are given the same registry; when nil, the retriever
	// creates one of its own at Init.
	Pools       *PoolRegistry
	logger      *slog.Logger
	columnNames map[string]string
	pool        *pgxpool.Pool
	flagset     *string
	URI         string
	Table       string
	status      retriever.Status
}

// Init connects to PostgreSQL and prepares the retriever for the flagset.
func (r *Retriever) Init(ctx context.Context, logger *slog.Logger, flagset *string) error {
	r.status = retriever.RetrieverNotReady

	r.logger = logger
	if r.logger == nil {
		r.logger = slog.New(slog.DiscardHandler)
	}

	r.columnNames = r.getColumnNames()
	r.flagset = flagset

	if r.Pools == nil {
		r.Pools = NewPoolRegistry()
	}

	if r.pool == nil {
		r.logger.InfoContext(ctx, "Initializing PostgreSQL retriever")
		r.logger.DebugContext(ctx, "Using columns", slog.Any("columns", r.columnNames))

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
			r.logger.ErrorContext(ctx, "Failed to scan row", slog.Any("error", scanErr))

			continue
		}

		// Parse the JSON config data into a map
		var config map[string]any

		unmarshalErr := json.Unmarshal(configData, &config)
		if unmarshalErr != nil {
			r.logger.ErrorContext(ctx, "Failed to unmarshal config data",
				slog.Any("error", unmarshalErr), slog.String("flagName", flagName))

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

	r.logger.DebugContext(ctx, "Retrieved flags from PostgreSQL", slog.Int("count", len(flagConfigs)))

	return result, nil
}

// OutputFormat declares that this retriever always returns JSON-encoded data,
// so the manager can pick the JSON parser regardless of the global FileFormat.
func (*Retriever) OutputFormat() string {
	return "json"
}

// buildQuery constructs the SQL query and arguments based on whether a flagset is specified.
// It uses pgx.Identifier to safely quote identifiers and prevent SQL injection.
func (r *Retriever) buildQuery() string {
	var query string

	flagNameCol := pgx.Identifier{r.columnNames[flagNameColumn]}.Sanitize()
	configCol := pgx.Identifier{r.columnNames[configKey]}.Sanitize()
	tableCol := pgx.Identifier{r.Table}.Sanitize()
	flagsetCol := pgx.Identifier{r.columnNames[flagsetKey]}.Sanitize()

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
	if r.flagset != nil && *r.flagset != "" && *r.flagset != stringsx.DefaultFlagSetName {
		return *r.flagset
	}

	return ""
}

// compile-time check of the interface implemented by the type.
var _ retriever.InitializableRetrieverWithFlagset = (*Retriever)(nil)
