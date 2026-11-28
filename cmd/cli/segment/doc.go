// Package segment is the semaphore-cli segment command group: import, add, remove and stat for the members of a
// dynamic segment in a Valkey or Redis store. The segment is named by its published definition (key, layout,
// connection and version key) and the connection by the bootstrap file, so nothing that those already say is
// typed again. No member id is printed or logged: only counts and line numbers are.
package segment
