//go:build !unix && !windows

package main

// ignoreSIGPIPE does nothing on a system that is neither Unix nor Windows.
func ignoreSIGPIPE() {}

// isBrokenPipe reports no broken pipe on a system that is neither Unix nor Windows.
func isBrokenPipe(error) bool { return false }
