// Package fileapi implements the authenticated HTTP adapter for Loader Engine.
// It exposes resumable uploads, files and access-group membership. Dynamic file
// sets remain behind engine contracts and the in-process Core API.
// The adapter calls Loader through a typed Go contract and never calls Core or
// accesses PostgreSQL, S3 or either engine's cache directly.
package fileapi
