package util

import (
	"github.com/pkg/errors"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
)

func IsNotFoundErr(err error) bool {
	statusErr, ok := errors.Cause(err).(*apierrors.StatusError)
	return ok && statusErr.Status().Reason == metav1.StatusReasonNotFound
}

func FilterOutErr(err error, fns ...utilerrors.Matcher) error {
	return utilerrors.FilterOut(err, fns...)
}

func FilterOutReasonNotFoundErr(err error) error {
	return FilterOutErr(err, func(err error) bool {
		return IsNotFoundErr(err)
	})
}
