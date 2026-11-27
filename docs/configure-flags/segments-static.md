# Segments - static

A static segment is a segment of type `file`: its members ship with the flag configuration and are held in the memory of the process as a hash set. See [Segments](segments.md) for what a segment is, the rule syntax and the three answers.

- Use it for small lists that change rarely (an internal testers list, a handful of accounts).
- A lookup is a hash lookup in memory: no network, so the answer is never unknown because of a store. It is unknown only when no provider holds the segment.
- Memory grows with the list. For a large or fast changing audience use a [dynamic segment](segments-dynamic.md).
- Freshness is the time of the last load of the member file.

## Member file

- One value per line. A UTF-8 byte order mark, blanks, carriage returns and blank lines are ignored; duplicates are one member.
- `segment.ReadMemberSet` reads the file into a set, bounded by `segment.Limits` (rows and bytes, with defaults).

## Use it in the Go module

```go
catalog, err := segment.NewCatalog(
    segment.Definition{Name: "beta-users", Type: segment.TypeFile},
)
// ...
client, err := ffclient.New(&ffclient.Config{
    Retriever: &fileretriever.Retriever{Path: "flags.yaml"},
    Segments:  catalog,
    SegmentProvider: segment.NewMemoryProvider(map[string][]string{
        "beta-users": {"alice", "carol"},
    }),
})
```

- `segment.NewMemoryProvider` and `segment.NewMemoryProviderFromSets` build the provider from member sets.
- Loading a member file from an S3 object, and the relay proxy settings, are not available yet (see [Segments](segments.md)).
