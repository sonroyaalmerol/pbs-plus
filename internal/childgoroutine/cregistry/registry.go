package cregistry

// HandlerFunc is the type for a function to execute in the child process.
type HandlerFunc func(args string)

// Entries is the static registry.
var Entries = map[string]HandlerFunc{}

