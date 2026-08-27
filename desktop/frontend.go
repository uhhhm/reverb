//go:build desktop

package main

// frontend bridge for Wails AssetServer — serves web/dist via wails.json.
// main() lives in main.go; this file provides the build-tag isolation for the desktop tag.
var desktopFrontend = true
