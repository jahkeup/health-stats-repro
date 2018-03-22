// Copyright 2018 Amazon.com, Inc. or its affiliates. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
//
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

//
// run the resulting build with:
//
// head -c 20 /dev/zero | xargs -0 -L1 -P0 ./health-stats-repro
//
// It may hang despite there being timeouts on some of the api calls
// being made to the daemon.
//
package main

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"time"

	docker "github.com/fsouza/go-dockerclient"
)

const (
	dockerfile = `
FROM busybox@sha256:5551dbdfc48d66734d0f01cafee0952cb6e8eeecd1e2492240bf2fd9640c2279
HEALTHCHECK --interval=1s --timeout=1s --retries=3 CMD echo hello
CMD ["sh", "-c", "sleep %s"]
`
	imageName                 = "docker-poke:healthchecks"
	imageSleepTimeString      = "2m"
	callTimeoutSecs      uint = 15
	runDuration               = time.Second * 10

	configStopContainer   = false
	configRemoveContainer = false
)

var (
	progT time.Time
)

func init() {
	progT = time.Now()
}

func main() {
	// Setup
	cl, err := docker.NewClientFromEnv()
	failOnError(err)

	log.Printf("| Config: stop container:\t%t", configRemoveContainer)
	log.Printf("| Config: remove container:\t%t", configRemoveContainer)

	err = cl.BuildImage(buildImageOptions(imageName))
	failOnError(err)

	// Repro case:
	//
	// It appears that when containers that run with healthchecks and
	// are listened on for stats, the api calls to the affected
	// containers hang.
	//
	// EDIT 2018-03-21: Stats streaming isn't necessary for the bug to manifest.

	// Create some containers
	cont1, err := createContainer(cl)
	failOnError(err)

	cont2, err := createContainer(cl)
	failOnError(err)

	// Start some containers
	err = cl.StartContainer(cont1.ID, nil)
	failOnError(err)

	err = cl.StartContainer(cont2.ID, nil)
	if err != nil {
		// stop the other container and then exit.
		stopAndCheckContainer(cl, cont1)
		failOnError(err)
	}

	conts := []*docker.Container{
		cont1,
		cont2,
	}

	// Run the containers for some time.
	log.Printf("Waiting for %s", runDuration)
	time.Sleep(runDuration)

	// Check the containers that were run.
	affected := []*docker.Container{}
	for _, cont := range conts {
		err = stopAndCheckContainer(cl, cont)
		if err != nil {
			affected = append(affected, cont)
		}
	}

	if len(affected) != 0 {
		log.Printf("Run affected %d container(s):", len(affected))
		for _, c := range affected {
			fmt.Printf("# docker inspect %s\n", c.ID)
		}
		os.Exit(2)
	}
}

func stopAndCheckContainer(client *docker.Client, cont *docker.Container) error {
	if configStopContainer {
		// Try to stop the container
		ctx, _ := context.WithTimeout(context.Background(), time.Duration(callTimeoutSecs)*time.Second)

		err := client.KillContainer(docker.KillContainerOptions{
			Context: ctx,
			ID:      cont.ID,
		})
		if err != nil {
			log.Printf("Could not stop container %q", cont.ID)
			log.Printf("Will try to inspect container %q", cont.ID)
		}
	}

	// Inspect run containers
	ctx, _ := context.WithTimeout(context.Background(), time.Duration(callTimeoutSecs)*time.Second)
	insp, err := client.InspectContainerWithContext(cont.ID, ctx)
	if err != nil {
		log.Printf("Error inspecting container: %s", err)
		return err
	}
	log.Printf("Successfully inspected container %q", insp.ID)

	if configRemoveContainer {
		log.Printf("Trying to remove container %q", insp.ID)
		err = client.RemoveContainer(docker.RemoveContainerOptions{
			ID: cont.ID,
		})
		if err != nil {
			log.Printf("Could not remove container %q", insp.ID)
			return err
		}
		log.Printf("Removed container %q", insp.ID)
	}
	return err
}

func logStatsForContainers(ctx context.Context, out io.Writer, client *docker.Client, containers ...*docker.Container) {
	statsChan := make(chan *docker.Stats)

	// stream stats from all containers until they stop.
	for x := range containers {
		id := containers[x].ID

		contStats := make(chan *docker.Stats)

		go client.Stats(docker.StatsOptions{
			Context: ctx,
			ID:      id,
			Stats:   contStats,
		})
		// combine stats logging for individual containers
		go func() {

			log.Printf("Listening for stats for container %q", id)
			for {
				select {
				case <-ctx.Done():
					return
				case stat, ok := <-contStats:
					if !ok {
						log.Printf("Container %q is no longer streaming", id)
						return
					}
					log.Printf("Received stat for container %q", id)
					statsChan <- stat
				}
			}
		}()
	}

	for {
		select {
		case <-ctx.Done():
			return
		case stat := <-statsChan:
			if stat == nil {
				continue
			}
			fmt.Fprintf(out, "%#v\n", stat)
		}
	}

}

func buildImageOptions(name string) docker.BuildImageOptions {
	log.Println("Building docker container for test")
	t := time.Now()
	inputbuf := bytes.NewBuffer(nil)
	tr := tar.NewWriter(inputbuf)
	data := bytes.NewBuffer(nil)

	fmt.Fprintf(data, dockerfile, imageSleepTimeString)

	tr.WriteHeader(&tar.Header{Name: "Dockerfile", Size: int64(len(data.Bytes())), ModTime: t, AccessTime: t, ChangeTime: t})
	tr.Write(data.Bytes())
	tr.Close()
	opts := docker.BuildImageOptions{
		Name:         name,
		InputStream:  inputbuf,
		OutputStream: os.Stdout,
	}
	return opts
}

func createContainer(client *docker.Client) (*docker.Container, error) {
	container, err := client.CreateContainer(docker.CreateContainerOptions{
		Config: &docker.Config{
			Image: imageName,
		},
	})

	return container, err
}

func logEvents(ctx context.Context, out io.Writer, events <-chan *docker.APIEvents) {
	for {
		select {
		case <-ctx.Done():
			return
		case event := <-events:
			fmt.Fprintf(out, "%#v", event)
		}
	}
}

func logFile(name string) io.WriteCloser {
	stamp := progT.Format(time.RFC3339)

	statsoutName := fmt.Sprintf("%s-%s", name, stamp)

	log.Printf("logging %q to %q", name, statsoutName)

	outfile, err := os.OpenFile(statsoutName, os.O_CREATE|os.O_WRONLY, 0640)
	failOnError(err)

	return outfile
}

func failOnError(err error) {
	if err != nil {
		log.Printf("%s", err)
		os.Exit(1)
	}
}
