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
CMD ["sh", "-c", "sleep 30m"]
`
	imageName            = "docker-poke:healthchecks"
	callTimeoutSecs uint = 10
	runDuration          = time.Second * 10
)

func buildImageOptions(name string) docker.BuildImageOptions {
	log.Println("Building docker container for test")
	t := time.Now()
	inputbuf := bytes.NewBuffer(nil)
	tr := tar.NewWriter(inputbuf)
	data := []byte(dockerfile)
	tr.WriteHeader(&tar.Header{Name: "Dockerfile", Size: int64(len(data)), ModTime: t, AccessTime: t, ChangeTime: t})
	tr.Write(data)
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

func main() {
	// Setup some log files
	statsout, eventsout := logFiles()

	cl, err := docker.NewClientFromEnv()
	failOnError(err)

	err = cl.BuildImage(buildImageOptions(imageName))
	failOnError(err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	eventChan := make(chan *docker.APIEvents)
	err = cl.AddEventListener(eventChan)
	failOnError(err)

	// Watch all events coming out of the daemon
	go logEvents(ctx, eventsout, eventChan)

	// Create some containers
	cont1, err := createContainer(cl)
	failOnError(err)

	cont2, err := createContainer(cl)
	failOnError(err)

	// Start those containers
	err = cl.StartContainer(cont1.ID, nil)
	failOnError(err)

	err = cl.StartContainer(cont2.ID, nil)
	if err != nil {
		// stop the other container and then exit.
		stopAndCheckContainer(cl, cont1)
		failOnError(err)
	}

	// And listen to their stats output.
	go logStatsForContainers(ctx, statsout, cl, cont1.ID, cont2.ID)

	// Run the containers for some time.
	log.Printf("Waiting for %s", runDuration)
	time.Sleep(runDuration)

	// Shut it down.
	cancel()

	// Check the containers that were run.
	success := true
	err = stopAndCheckContainer(cl, cont1)
	if err != nil {
		success = false
	}
	err = stopAndCheckContainer(cl, cont2)
	if err != nil {
		success = false
	}

	if !success {
		os.Exit(2)
	}
}

func stopAndCheckContainer(client *docker.Client, cont *docker.Container) error {
	err := client.StopContainer(cont.ID, callTimeoutSecs)
	if err != nil {
		log.Printf("Could not stop container %q", cont.ID)
		log.Printf("Will try to inspect container %q", cont.ID)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(callTimeoutSecs)*time.Second)

	insp, err := client.InspectContainerWithContext(cont.ID, ctx)
	cancel()
	if err != nil {
		return err
	}
	log.Printf("Successfully inspected container %q", insp.ID)

	log.Printf("Removing container %q", insp.ID)
	err = client.RemoveContainer(docker.RemoveContainerOptions{
		ID: cont.ID,
	})
	return err
}

func logStatsForContainers(ctx context.Context, out io.Writer, client *docker.Client, containerIDs ...string) {
	statsChan := make(chan *docker.Stats)

	// stream stats from all containers until they stop.
	for x := range containerIDs {
		id := containerIDs[x]

		contStats := make(chan *docker.Stats)

		go client.Stats(docker.StatsOptions{
			Context: ctx,
			ID:      id,
			Stats:   contStats,
		})
		// combine stats logging for individual containers
		go func() {

			log.Printf("listing for stats for container %q", id)
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

func logFiles() (io.WriteCloser, io.WriteCloser) {
	t := time.Now()
	stamp := t.Format(time.RFC3339)
	statsoutName := fmt.Sprintf("statsout-%s", stamp)
	eventsoutName := fmt.Sprintf("eventsout-%s", stamp)

	log.Printf("logging events to %q", eventsoutName)
	log.Printf("logging stats to %q", statsoutName)

	statsout, err := os.OpenFile(statsoutName, os.O_CREATE|os.O_WRONLY, 0640)
	failOnError(err)
	eventsout, err := os.OpenFile(eventsoutName, os.O_CREATE|os.O_WRONLY, 0640)
	failOnError(err)

	return statsout, eventsout
}

func failOnError(err error) {
	if err != nil {
		log.Printf("%s", err)
		os.Exit(1)
	}
}
