N := 10 # run count: make run N=20

repro-runner: main.go
	go build -o repro-runner .

run: repro-runner
	head -c $(N) /dev/zero | xargs -0 -L1 -P0 ./repro-runner || X=$$?; \
		echo Exited $$X; exit $$X

clean:
	rm -f repro-runner *out-*
