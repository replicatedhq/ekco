package util

import (
	"github.com/pkg/errors"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
)

func IsNotFoundErr(err error) bool {
	return k8serrors.IsNotFound(err)
}

func IsAlreadyExists(err error) bool {
	return k8serrors.IsAlreadyExists(err)
}

func FilterOutErr(err error, fns ...utilerrors.Matcher) error {
	return utilerrors.FilterOut(errors.Cause(err), fns...)
}

func FilterOutReasonNotFoundErr(err error) error {
	return FilterOutErr(err, func(err error) bool {
		return IsNotFoundErr(err)
	})
}
