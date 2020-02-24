package main

import (
	"log"
	"math/rand"
	"time"

	"github.com/replicatedhq/ekco/cmd/ekco/cli"
)

func main() {
	rand.Seed(time.Now().UnixNano())

	if err := cli.InitAndExecute(); err != nil {
		log.Fatal(err)
	}
}
