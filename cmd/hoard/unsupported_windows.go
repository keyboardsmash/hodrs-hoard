// Höðrs Hoard does not support Windows (see docs/spec.md, section 8).
//
// The `//go:build windows` line below means this file is only compiled when
// building for Windows. It refers to a name that doesn't exist, so a Windows
// build stops with:
//
//	undefined: hoard_does_not_support_windows
//
// A file name ending in _windows.go would do the same on its own; the
// explicit build line makes the intent obvious.

//go:build windows

package main

var _ = hoard_does_not_support_windows
