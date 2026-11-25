# Postgresql

## Overview

## Postgresql Table Format

If you use Postgresql to store your flags, you need a specific format to store your flags.

**The table should have the following columns:**

- `flag_name`: the name of your feature flag.
- `flagset`: the name of your flagset, if you don't use a flagset, you can leave this column empty.
- `config`: the configuration of your feature flag in JSON format.

> [!NOTE]
> You can customize the column names by configuring the retriever options. The example above uses the default column names that works out of the box.
>
> You can also add more columns to the table for your own needs, but only those 3 fields are mandatory to work with the Semaphore retriever.

```sql
-- ----------------------------------------------------------------------------
-- GO FEATURE FLAG POSTGRESQL RETRIEVER INITIALIZATION SCRIPT
-- This script is used to initialize the PostgreSQL database for the Semaphore retriever.
-- This is a very minimal setup for the retriever, you can add more columns or constraints to the table if you need to.
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

You can use the [init_table.sql](https://github.com/BlackbirdWorks/semaphore/blob/main/retriever/postgresqlretriever/testdata/sql/init.sql) file as a reference to create your table.

## Filter on flagset

The  retriever, is **flagset** aware. This means that when you are using flagsets, the retriever will only retrieve the flags for the specific flagset.  
To do this, the retriever will use the **flagset name** in your flagset configuration to add a `WHERE` clause to the query and filter on the `flagset` column.

> [!WARNING]
> When you are using flagsets, be sure to use the same name in your flagset configuration as the one used in the `flagset` column in your postgres table.

## Configure the relay proxy

To configure your relay proxy to use the  retriever, you need to add the following
configuration to your relay proxy configuration file:

```yaml
# ...
retrievers:
  - kind: postgresql
    uri: "postgres://user:password@localhost:5432/dbname"
    table: "go_feature_flag"
# ...
```
| Field name |    Mandatory     | Type   | Default  | Description                                                                                                                                                                                                                           |
| ---------- | :-----------: | ------ | -------- | ----------------------------------------------------------------------------------------------------------------- |
| `kind`     |  Yes   | string | **none** | **Value should be `postgresql`**.<br/>_This field is mandatory and describes which retriever you are using._                                                                                                                               |
| `uri`      |  Yes   | string | **none** | Connection URI of your postgres instance.                                                                                                                              |
| `table`    |  Yes   | string | **none** | Name of the table where your flags are stored.                                                                                                                      |
| `columns`  | No | object | **none** | Custom column name mapping. See [How to use custom column names](#how-to-use-custom-column-names) for more details.                                                                                                                       |

### How to use custom column names

You can use custom column names by configuring the `columns` field.

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

The `columns` field, is a map of the default column names to the custom column names in your postgres table.

## Configure the GO Module
To configure your GO module to use the  retriever, you need to add the following
configuration to your `ffclient.Config{}` object:

```go
client, err := ffclient.New(&ffclient.Config{
    PollingInterval: 3 * time.Second,
	  Retriever: &postgresqlretriever.Retriever{
      URI: "postgres://user:password@localhost:5432/dbname",
      Table: "go_feature_flag",
      Columns: map[string]string{
        "flag_name": "name",
        "flagset": "namespace",
        "config": "settings",
      },
    },
})
defer client.Close()
```

| Field         |    Mandatory     | Description                                                                                                                                      |
|---------------|:----------------:|--------------------------------------------------------------------------------------------------------------------------------------------------|
| `uri`      |  Yes   | Connection URI of your postgres instance.                                                                                                                              |
| `table`    |  Yes   | Name of the table where your flags are stored.                                                                                                                      |
| `columns`  | No | Custom column name mapping. See [How to use custom column names](#how-to-use-custom-column-names) for more details.                                                                                                                       |
