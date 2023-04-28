package overrides

import (
	"sync"
)

var changeMut = sync.Mutex{}

var prometheus = false

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
