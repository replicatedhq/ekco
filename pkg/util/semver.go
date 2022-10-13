package util

import "github.com/blang/semver"

var semverZero = semver.Version{}

func SemverIsZero(version semver.Version) bool {
	return semverZero.Equals(version)
}
