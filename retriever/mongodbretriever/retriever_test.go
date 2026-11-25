//go:build docker

package mongodbretriever_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/mongodb"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/integration/mtest"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/blackbirdworks/semaphore/retriever"
	"github.com/blackbirdworks/semaphore/retriever/mongodbretriever"
	"github.com/blackbirdworks/semaphore/testutils"
	"github.com/blackbirdworks/semaphore/utils/fflog"
)

func Test_MongoDBRetriever_Retrieve(t *testing.T) {
	t.Parallel()

	mtDB := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))

	tests := []struct {
		name string
		data string
		want []byte
	}{
		{
			name: "Returns well formed flag definition document",
			data: testutils.MongoFindResultString,
			want: []byte(testutils.QueryResult),
		},
		{
			name: "One of the Flag definition document does not have 'flag' key/value (ignore this document)",
			data: testutils.MongoMissingFlagKey,
			want: []byte(testutils.MissingFlagKeyResult),
		},
		{
			name: "Flag definition document 'flag' key does not have 'string' value (ignore this document)",
			data: testutils.MongoFindResultFlagNoStr,
			want: []byte(testutils.FlagKeyNotStringResult),
		},
		{
			name: "No flags found on DB",
			want: []byte("{}"),
		},
	}
	for _, tt := range tests {
		mtDB.Run(tt.name, func(t *mtest.T) {
			mongodbContainer, err := mongodb.Run(context.TODO(), "mongo:6")
			testcontainers.CleanupContainer(t, mongodbContainer)
			require.NoError(t, err)

			uri, err := mongodbContainer.ConnectionString(context.Background())
			require.NoError(t, err)

			if tt.data != "" {
				// insert data
				client, connectErr := mongo.Connect(context.TODO(), options.Client().ApplyURI(uri))
				require.NoError(t, connectErr)
				coll := client.Database("database").Collection("collection")
				var documents []bson.M
				connectErr = json.Unmarshal([]byte(tt.data), &documents)
				require.NoError(t, connectErr)

				for _, doc := range documents {
					_, insertOneErr := coll.InsertOne(context.TODO(), doc)
					require.NoError(t, insertOneErr)
				}
			}

			// retriever
			mdb := mongodbretriever.Retriever{
				URI:        uri,
				Collection: "collection",
				Database:   "database",
			}
			assert.Equal(t, retriever.RetrieverNotReady, mdb.Status())
			err = mdb.Init(context.TODO(), &fflog.FFLogger{})
			require.NoError(t, err)
			defer func() {
				require.NoError(t, mdb.Shutdown(context.TODO()))
				assert.Equal(t, retriever.RetrieverNotReady, mdb.Status())
			}()
			assert.Equal(t, retriever.RetrieverReady, mdb.Status())

			got, err := mdb.Retrieve(context.Background())
			require.NoError(t, err)
			if tt.want == nil {
				assert.Nil(t, got)
			} else {
				modifiedGot, removeIDFromJSONErr := removeIDFromJSON(string(got))
				require.NoError(t, removeIDFromJSONErr)
				assert.JSONEq(t, string(tt.want), modifiedGot)
			}
		})
	}
}

func Test_MongoDBRetriever_InvalidURI(t *testing.T) {
	t.Parallel()

	mdb := mongodbretriever.Retriever{
		URI:        "invalidURI",
		Collection: "collection",
		Database:   "database",
	}
	assert.Equal(t, retriever.RetrieverNotReady, mdb.Status())
	err := mdb.Init(context.TODO(), &fflog.FFLogger{})
	require.Error(t, err)
	assert.Equal(t, retriever.RetrieverError, mdb.Status())
}

func Test_MongoDBRetriever_OutputFormat(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		want string
	}{
		{
			name: "returns json format hint for manager parser selection",
			want: "json",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mdb := mongodbretriever.Retriever{}
			assert.Equal(t, tt.want, mdb.OutputFormat())
		})
	}
}

func removeIDFromJSON(jsonStr string) (string, error) {
	var data any
	err := json.Unmarshal([]byte(jsonStr), &data)
	if err != nil {
		return "", err
	}

	removeIDFields(data)

	modifiedJSON, err := json.Marshal(data)
	if err != nil {
		return "", err
	}

	return string(modifiedJSON), nil
}

func removeIDFields(data any) {
	switch v := data.(type) {
	case map[string]any:
		delete(v, "_id")
		for _, value := range v {
			removeIDFields(value)
		}
	case []any:
		for _, item := range v {
			removeIDFields(item)
		}
	}
}
