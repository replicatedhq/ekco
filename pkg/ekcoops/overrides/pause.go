package overrides

import (
	"sync"
)

var changeMut = sync.Mutex{}

var prometheus = false
var minio = false
var kotsadm = false

func PausePrometheus() {
	changeMut.Lock()
	defer changeMut.Unlock()

	prometheus = true
}

func ResumePrometheus() {
	changeMut.Lock()
	defer changeMut.Unlock()

	prometheus = false
}

func PrometheusPaused() bool {
	changeMut.Lock()
	defer changeMut.Unlock()

	return prometheus
}

func PauseMinIO() {
	changeMut.Lock()
	defer changeMut.Unlock()

	minio = true
}

func ResumeMinIO() {
	changeMut.Lock()
	defer changeMut.Unlock()

	minio = false
}

func MinIOPaused() bool {
	changeMut.Lock()
	defer changeMut.Unlock()

	return minio
}

func PauseKotsadm() {
	changeMut.Lock()
	defer changeMut.Unlock()

	kotsadm = true
}

func ResumeKotsadm() {
	changeMut.Lock()
	defer changeMut.Unlock()

	kotsadm = false
}

func KotsadmPaused() bool {
	changeMut.Lock()
	defer changeMut.Unlock()

	return kotsadm
}
