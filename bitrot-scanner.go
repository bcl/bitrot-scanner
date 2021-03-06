package main

import "fmt"
import "github.com/kormoc/ionice"
import "github.com/nightlyone/lockfile"
import "github.com/vbauerster/mpb"
import "github.com/vbauerster/mpb/cwriter"
import "os"
import "path/filepath"
import "sync"
import "syscall"
import flag "github.com/ogier/pflag"

var lock lockfile.Lockfile
var progressBar *mpb.Progress

// channel for jobs
var jobs chan job

type job struct {
	path string
	info os.FileInfo
	err  error
}

func main() {
	processFlags()

	setupLogs()

	if version {
		fmt.Printf("Version: %v\n", Version)
		return
	}

	if lockfilePath != "" {
		var err error
		if lock, err = lockfile.New(lockfilePath); err != nil {
			Error.Fatalf("Lockfile failed. reason: %v", err)
		}
		if err := lock.TryLock(); err != nil {
			Error.Fatalf("Lockfile failed. reason: %v", err)
		}
		defer lock.Unlock()
	}

	if err := syscall.Setpriority(syscall.PRIO_PROCESS, os.Getpid(), nice); err != nil {
		Warn.Println("Setting nice failed.")
	}

	if err := ionice.IONiceSelf(uint32(ioniceClass), uint32(ioniceClassdata)); err != nil {
		Warn.Println("Setting ionice failed.")
	}

	if enableProgressBar {
		if workerCount > 1 {
			Warn.Println("-progressBar requires -workerCount=1 with -debug or -verbose, disabling -progressBar")
		} else {
			width, _, _ := cwriter.GetTermSize()
			progressBar = mpb.New().SetWidth(width)
		}

	}

	var workerFunc filepath.WalkFunc

	if resetXattrs {
		workerFunc = workerReset
	} else {
		filterChecksumAlgos()
		workerFunc = workerChecksum
	}

	var jobsQueueSize = workerCount
	if workerCount < 100 {
		jobsQueueSize = 100
	}

	jobs = make(chan job, jobsQueueSize)

	// start workers
	wg := &sync.WaitGroup{}
	for i := 1; i <= workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			for {
				j, ok := <-jobs
				if !ok {
					return
				}
				err := workerFunc(j.path, j.info, j.err)
				if err != nil {
					Error.Println(err)
				}
			}
		}()
	}

	// Loop over the passed in directories and hash and/or validate

	for _, path := range flag.Args() {
		Info.Printf("Processing %v...\n", path)
		if err := filepath.Walk(path, enqueuePath); err != nil {
			Error.Println(err)
		}
	}
	close(jobs)
	wg.Wait()

	if progressBar != nil {
		progressBar.Stop()
	}
}
