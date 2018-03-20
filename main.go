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
	imageName = "docker-poke:healthchecks"
)

func buildImageOptions(name string) docker.BuildImageOptions {
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

	cl, err := docker.NewClientFromEnv()
	failOnError(err)

	err = cl.BuildImage(buildImageOptions(imageName))
	failOnError(err)
	defer cl.RemoveImage(imageName)

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
	defer cl.StopContainer(cont1.ID, 0)

	err = cl.StartContainer(cont2.ID, nil)
	failOnError(err)
	defer cl.StopContainer(cont2.ID, 0)

	// And listen to their stats output.
	go statsForContainers(ctx, statsout, cl, cont1.ID, cont2.ID)

	time.Sleep(10 * time.Second)
}

func failOnError(err error) {
	if err != nil {
		fail("%s", err)
	}
}

func fail(fstr string, v ...interface{}) {
	log.Printf(fstr, v...)
	os.Exit(1)
}

func statsForContainers(ctx context.Context, out io.Writer, client *docker.Client, containerIDs ...string) {
	statsChan := make(chan *docker.Stats)

	for x := range containerIDs {
		idx := x
		id := containerIDs[x]

		// Super aggressively fetch stats from Docker.
		go func() {
			contStats := make(chan *docker.Stats)
			subscribeAgain := make(chan bool, 1)
			subscribeAgain <- true

			log.Printf("listing for stats for container %q", id)
			for {
				select {
				case <-ctx.Done():
					return
				case stat := <-contStats:
					if stat == nil {
						continue
					}
					log.Printf("Received stat for container %q id %q", idx, id)
					statsChan <- stat
					subscribeAgain <- true
				case <-subscribeAgain:
					log.Printf("listening for more stats")
					contStats = make(chan *docker.Stats)

					go func() {
						client.Stats(docker.StatsOptions{
							Context: ctx,
							ID:      id,
							Stats:   contStats,
						})
						<-subscribeAgain
					}()
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
