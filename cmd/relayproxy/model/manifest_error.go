package model

type ManifestError struct {
	ErrorDetails ManifestErrorDetails `json:"error"`
}

type ManifestErrorDetails struct {
	Message string `example:"Authorization header required" json:"message"`
	Status  int    `example:"401"                           json:"status"`
}
