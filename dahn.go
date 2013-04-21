package main

import (
	_ "errors"
	"flag"
	"github.com/howeyc/fsnotify"
	"log"
	"net/url"
	_ "os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

var watchedFN = flag.String("file", "", "This is the file to monitor for changes.")

func extractRemoteName(fn string) (string, error) {
	//read first line of file, strip starting slashes, return it
	rBytes, err := (*exec.Command("head", "-n", "1", fn)).Output()
	result := strings.TrimSpace(string(rBytes))
	result = strings.TrimLeft(result, "/")
	return result, err
}

func mount(r string) error {
	//extract relevant information from remote
	remote, _ := url.Parse(r)
	username := (*remote.User).Username()
	password, _ := (*remote.User).Password()
	auth := ""
	if len(username) > 0 {
		auth = username
		if len(password) > 0 {
			auth = auth + ":" + password
		}
		auth = auth + "@"
	}
	mountLoc := remote.Scheme + "://" + auth + remote.Host
	//execute gvfs-mount to mount the location
	return (*exec.Command("gvfs-mount", mountLoc)).Run()
}

func attemptCopy(local string, remote string) error {
	return (*exec.Command("gvfs-copy", local, remote)).Run()
}

func remoteCopy(local string, remote string) error {
	err := attemptCopy(local, remote)
	if err != nil {
		mountErr := mount(remote)
		if mountErr != nil {
			return mountErr
		}
	}
	err = attemptCopy(local, remote)
	if err != nil {
		return err
	}
	return nil
}

func localCopy(main string, remote string) (string, error) {
	//use the username embedded in "remote" to make a copy of the file locally
	//sibling to "main", <remote:username>.styl
	parsed, _ := url.Parse(remote)
	user := (*(*parsed).User).Username()
	localFile := filepath.Dir(main) + "/" + user + ".styl"
	//log.Println("Copies: ", main, localFile)
	err := (*exec.Command("cp", main, localFile)).Run()
	if err != nil {
		return "", err
	}
	return localFile, nil
}

func compile(file string) (string, error) {
	//run stylus against the file
	re, _ := regexp.Compile("(.*)" + filepath.Ext(file) + "$")
	cssFile := re.FindStringSubmatch(file)[1]
	cssFile = cssFile + ".css"
	err := (*exec.Command("stylus", file)).Run()
	if err != nil {
		return "", err
	}
	return cssFile, nil
}

func processFile(proxy string) error {
	//create a proxy file to be watched
	remoteCopyName, _ := extractRemoteName(proxy)
	//log.Println("remote target: ", remoteCopyName)
	localFN, err := localCopy(proxy, remoteCopyName)
	if err != nil {
		log.Fatal(err)
	}
	//log.Println("local copy: ", localFN)
	compiledFile, _ := compile(localFN)
	//log.Println("compiled file: ", compiledFile)
	remoteCopy(compiledFile, remoteCopyName)
	log.Println("saved")
	return nil
}

func fileProcessor(process chan bool, proxy string) chan bool {
	done := make(chan bool)
	go func() {
		for {
			select {
			case <-process:
				processFile(proxy)
				done <- true
			}
		}
	}()
	return done
}

func main() {
	log.SetFlags(log.Ltime)
	flag.Parse()
	watcher, werr := fsnotify.NewWatcher()

	if werr != nil {
		log.Fatal(werr)
	}

	start := make(chan bool)
	finished := fileProcessor(start, *watchedFN)

	processing := false
	shouldStart := false

	func() {
		werr = watcher.Watch(*watchedFN)
		if werr != nil {
			log.Fatal(werr)
		}

		for {
			select {
			case ev := <-watcher.Event:
				log.Println(ev)
				if ev.IsDelete() {
					watcher.RemoveWatch(*watchedFN)
					werr = watcher.Watch(*watchedFN)
					if werr != nil {
						log.Fatal(werr)
						break
					}
				} else {
					if processing {
						shouldStart = true
					} else {
						shouldStart = false
						start <- true
						processing = true
					}
				}
			case <-finished:
				if shouldStart {
					shouldStart = false
					start <- true
					processing = true
				} else {
					processing = false
					shouldStart = false
				}
			case err := <-watcher.Error:
				log.Println("error: ", err)
				break
			}
		}
	}()

	watcher.Close()
}
