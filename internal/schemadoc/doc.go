// Package schemadoc turns the JSON Schemas of the repository into the Markdown schema reference of the documentation.
//
// Keys flattens a schema into one entry per key path. Render writes the reference page from the schemas of the
// bootstrap file and of the control-plane documents. ControlPlaneSchemas generates the schemas of the control-plane
// documents from the Go types of the controlplane package and their comments.
package schemadoc
