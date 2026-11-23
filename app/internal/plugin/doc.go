// Package plugin loads manifests and runs plugins in moejs runtimes behind
// one Go interface: the host object, permissions, and the Go types of the
// plugin contract, which every answer is validated against before a page
// sees it.
package plugin
