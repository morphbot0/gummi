//go:build !unix

package skillpack

// flock is no lock where flock is not: the in-process mutex still holds.
func flock(string) func() { return func() {} }
