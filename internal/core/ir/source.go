// Package ir defines AlpineForm's provider-independent intermediate representation.
package ir

// SourceRef identifies the declaration that produced a value or resource.
type SourceRef struct {
	File string `json:"file,omitempty"`
	Line int    `json:"line,omitempty"`
	Path string `json:"path,omitempty"`
}
