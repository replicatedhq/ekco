package main

import (
	"github.com/replicatedhq/ekco/cmd/ekco/cli"
	"log"
	"math/rand"
	"time"
)

func main() {
	rand.New(rand.NewSource(time.Now().UnixNano()))
	
	if err := cli.InitAndExecute(); err != nil {
		log.Fatal(err)
	}
}
