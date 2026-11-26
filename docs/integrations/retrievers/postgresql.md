# PostgreSQL

## Overview

Load flag configurations from a PostgreSQL table.

## PostgreSQL table format

Your table must have these three columns:

- `flag_name`: name of your feature flag.
- `flagset`: name of your flagset (can be empty if you do not use flagsets).
- `config`: feature flag configuration in JSON format.

> [!NOTE]
> You can customize these column names in the retriever configuration. The example below uses the default column names, which work out of the box.
>
> You may add extra columns for your own needs; only these three columns are required by Semaphore.

```sql
-- ----------------------------------------------------------------------------
-- SEMAPHORE POSTGRESQL RETRIEVER INITIALIZATION SCRIPT
-- Minimal setup for the Semaphore PostgreSQL retriever.
-- You can add extra columns or constraints to the table as needed.
-- ----------------------------------------------------------------------------

-- Enable UUID extension if not already enabled
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

-- Create the go_feature_flag table
CREATE TABLE IF NOT EXISTS go_feature_flag (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    flag_name VARCHAR(255) NOT NULL,
    flagset VARCHAR(255) NOT NULL,
    config JSONB NOT NULL
);

-- Create indexes for better query performance
CREATE INDEX IF NOT EXISTS idx_go_feature_flag_flagset ON go_feature_flag(flagset);
CREATE INDEX IF NOT EXISTS idx_go_feature_flag_flag_name ON go_feature_flag(flag_name);
CREATE INDEX IF NOT EXISTS idx_go_feature_flag_flagset_flag_name ON go_feature_flag(flagset, flag_name);

-- Add a unique constraint to prevent duplicate flags in the same flagset
ALTER TABLE go_feature_flag ADD CONSTRAINT unique_flag_per_flagset UNIQUE (flag_name, flagset);
```

See [init.sql](https://github.com/BlackbirdWorks/semaphore/blob/main/retriever/postgresqlretriever/testdata/sql/init.sql) for the reference SQL file.

## Filter on flagset

The PostgreSQL retriever is **flagset-aware**. When flagsets are enabled, it filters rows with a `WHERE` clause matching your configured **flagset name** against the `flagset` column.

> [!WARNING]
> Ensure the flagset name in your Semaphore configuration matches the value stored in the PostgreSQL `flagset` column.

## Configure the relay proxy

Add this configuration to your relay proxy config file:

```yaml
# ...
retrievers:
  - kind: postgresql
    uri: "postgres://user:password@localhost:5432/dbname"
    table: "go_feature_flag"
# ...
```

| Field name | Mandatory | Type   | Default  | Description |
|------------|:---------:|--------|----------|-------------|
| `kind`     | Yes       | string | **none** | **Value should be `postgresql`.**<br/>_Mandatory field identifying the retriever._ |
| `uri`      | Yes       | string | **none** | Connection URI of your PostgreSQL instance. |
| `table`    | Yes       | string | **none** | Name of the table storing your flags. |
| `columns`  | No        | object | **none** | Custom column name mapping. See [How to use custom column names](#how-to-use-custom-column-names). |

### How to use custom column names

Use `columns` to map the default column names to custom column names in your PostgreSQL table:

```yaml
# ...
retrievers:
  - kind: postgresql
    uri: "postgres://user:password@localhost:5432/dbname"
    table: "go_feature_flag"
    columns:
      flag_name: "name"
      flagset: "namespace"
      config: "settings"
```

## Configure the Go module

Add the retriever to `ffclient.Config{}`:

```go
client, err := ffclient.New(&ffclient.Config{
    PollingInterval: 3 * time.Second,
    Retriever: &postgresqlretriever.Retriever{
        URI:   "postgres://user:password@localhost:5432/dbname",
        Table: "go_feature_flag",
        Columns: map[string]string{
            "flag_name": "name",
            "flagset":   "namespace",
            "config":    "settings",
        },
    },
})
defer client.Close()
```

| Field         | Mandatory | Description |
|---------------|:---------:|-------------|
| **`URI`**     | Yes       | Connection URI of your PostgreSQL instance. |
| **`Table`**   | Yes       | Name of the table storing your flags. |
| **`Columns`** | No        | Custom column name mapping (`map[string]string`). See [How to use custom column names](#how-to-use-custom-column-names). |
