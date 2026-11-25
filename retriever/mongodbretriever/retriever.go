package mongodbretriever

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/blackbirdworks/semaphore/retriever"
	"github.com/blackbirdworks/semaphore/utils/fflog"
)

var (
	errDatabaseConnectionIsNot = errors.New("database connection is not initialized")
)

// Retriever is a configuration struct for a MongoDB connection and Collection.
type Retriever struct {
	dbConnection *mongo.Database
	dbClient     *mongo.Client
	logger       *fflog.FFLogger
	// MongoDB connection URI
	URI string
	// Mongodb database where flags collection is
	Database string
	// Mongodb collection where flag definitions are stored
	Collection string
	status     string
	// mu guards the connection: Shutdown can run while a Retrieve (a forced refresh) is still in flight.
	mu sync.RWMutex
}

// Init is initializing the retriever to start fetching the flags configuration.
func (r *Retriever) Init(ctx context.Context, logger *fflog.FFLogger) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.logger = logger
	if r.dbConnection == nil {
		r.status = retriever.RetrieverNotReady

		client, err := mongo.Connect(ctx, options.Client().ApplyURI(r.URI))
		if err != nil {
			r.status = retriever.RetrieverError

			return err
		}
		r.dbClient = client
		r.dbConnection = client.Database(r.Database)
		r.status = retriever.RetrieverReady
	}

	return nil
}

// Status returns the current status of the retriever.
func (r *Retriever) Status() retriever.Status {
	if r == nil {
		return retriever.RetrieverNotReady
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.status == "" {
		return retriever.RetrieverNotReady
	}

	return r.status
}

// Shutdown disconnects the retriever from Mongodb instance.
func (r *Retriever) Shutdown(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.dbClient == nil {
		r.status = retriever.RetrieverNotReady

		return nil
	}
	err := r.dbClient.Disconnect(ctx)
	// the client is dropped whatever the outcome of the disconnection: the retriever can not be used anymore
	r.dbClient = nil
	r.dbConnection = nil
	r.status = retriever.RetrieverNotReady

	return err
}

// Retrieve Reads flag configuration from mongodb and returns it
// if a document does not comply with the specification it will be ignored.
func (r *Retriever) Retrieve(ctx context.Context) ([]byte, error) {
	r.mu.RLock()
	dbConnection := r.dbConnection
	r.mu.RUnlock()
	if dbConnection == nil {
		return nil, errDatabaseConnectionIsNot
	}

	opt := options.CollectionOptions{}
	opt.SetBSONOptions(&options.BSONOptions{OmitZeroStruct: true})

	coll := dbConnection.Collection(r.Collection, &opt)

	cursor, err := coll.Find(ctx, bson.D{})
	if err != nil {
		return nil, err
	}
	defer func() { _ = cursor.Close(ctx) }()

	ffDocs := make(map[string]bson.M)

	for cursor.Next(ctx) {
		var doc bson.M

		if decodeErr := cursor.Decode(&doc); decodeErr != nil {
			return nil, decodeErr
		}

		if val, ok := doc["flag"]; ok {
			delete(doc, "flag")
			if str, isString := val.(string); isString {
				ffDocs[str] = doc
			} else {
				r.logger.ErrorContext(ctx, "flag key does not have a string as value")
			}
		} else {
			r.logger.ErrorContext(ctx, "no 'flag' entry found")
		}
	}

	flags, err := json.Marshal(ffDocs)
	if err != nil {
		return nil, err
	}

	return flags, nil
}

// OutputFormat declares that this retriever always returns JSON-encoded data,
// so the manager can pick the JSON parser regardless of the global FileFormat.
func (r *Retriever) OutputFormat() string {
	return "json"
}
