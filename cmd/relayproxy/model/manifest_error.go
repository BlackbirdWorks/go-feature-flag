package model

// ManifestError is the error response of the flag manifest API.
type ManifestError struct {
	ErrorDetails ManifestErrorDetails `json:"error"`
}

// ManifestErrorDetails holds the details of a flag manifest error.
type ManifestErrorDetails struct {
	Message string `example:"Authorization header required" json:"message"`
	Status  int    `example:"401"                           json:"status"`
}
