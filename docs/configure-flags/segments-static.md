# Segments - static

A static segment is a segment of type `file`: its members ship with the flag configuration and are held in the memory of the process as a hash set. See [Segments](segments.md) for what a segment is, the rule syntax and the two answers (member, not a member).

- Use it for small lists that change rarely (an internal testers list, a handful of accounts).
- A lookup is a hash lookup in memory: no network, so the answer is never missing because of a store. It is not a member when no provider holds the segment (unknown only for a segment with `onError: unknown`, see [Failure policy of a segment](segments.md#failure-policy-of-a-segment)).
- Memory grows with the list. For a large or fast changing audience use a [dynamic segment](segments-dynamic.md).
- Freshness is the time of the last load of the member file.

## In the relay proxy

A flag set lists the `Segments` documents that its rules can use in `segmentSources`. When the relay proxy builds the flag set it makes one catalog of the segments of those documents (a later source wins on a repeated name). The members of a static segment are read once for each version of the document, held as a hash set in memory and shared by the flag sets that list the document. A rule over a static segment is answered from that set: no call to a provider and no allocation more than the same rule over an `in [...]` list. A reload that changes the members builds a new catalog for the flag sets that list the document, and the next request uses it; a flag set that does not list the document is not rebuilt. `/info` shows, for each flag set, how many static and dynamic segments it has (`segments`, left out when it has none). A segment source that is not a loaded `Segments` object is a problem of the flag set, and the flag set is degraded.

## Published form

In a flag set published through the control plane, a static segment is a definition with inline `members` in a
document of kind `Segments` (see [the document format](../go-module/controlplane.md#segments-document)):

<!-- segments-doc -->
```yaml
apiVersion: semaphore/v1
kind: Segments
segments:
  - name: beta-testers
    members:
      - alice
      - bob
  - name: nobody-yet
    members: []
```

Duplicates are rejected, an empty list is allowed, and the list is bounded (100000 members, 4 MiB).

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
