// Package freespace reports the number of bytes available on the filesystem
// holding a path. The engine uses it to pre-flight the staging directory, so
// a backup fails at second zero with an actionable message instead of dying
// halfway through a multi-GB archive on a small overlay partition.
package freespace
