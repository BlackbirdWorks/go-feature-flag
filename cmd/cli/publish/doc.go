// Package publish implements "semaphore-cli publish": it reads a directory of flag files and flag-set documents,
// builds the manifest and publishes both to S3 with controlplane/s3publish, and "semaphore-cli publish verify",
// which checks what the bucket holds.
package publish
