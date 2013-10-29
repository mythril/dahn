package main

//potential improvements:
//backing up the styl files after modification
//unmount the mount point after 1 hour of inactivity

import (
	_"errors"
	_"flag"
	"github.com/howeyc/fsnotify"
	"log"
	"net/url"
	"os"
	"io"
	"os/exec"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"hash/fnv"
)

//var watchedFN = flag.String("file", "", "This is the file to monitor for changes.")
var watchedFN = os.Args[1]

func extractRemoteName(fn string) (string, error) {
	//read first line of file, strip starting slashes, return it
	rBytes, err := (*exec.Command("head", "-n", "1", fn)).Output()
	result := strings.TrimSpace(string(rBytes))
	result = strings.TrimLeft(result, "/")
	return result, err
}

func extractMountPoint(r string) string {
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
	return remote.Scheme + "://" + auth + remote.Host
}

func isMounted(softDevice string) bool {
	//execute gvfs-mount to validate the mount
	cmd := fmt.Sprintf("bash -c 'ex=`gvfs-mount -l|grep \"%s\"|wc -l`;echo $((1 - $ex))'", softDevice)
	return (*exec.Command(cmd)).Run() == nil
}

func mount(softDevice string) error {
	//execute gvfs-mount to mount the location
	return (*exec.Command("gvfs-mount", softDevice)).Run()
}

func attemptCopy(src string, target string) error {
	return (*exec.Command("gvfs-copy", src, target)).Run()
}

func hashed(fn string) string {
	hasher := fnv.New32()
	hasher.Write([]byte(fn))
	return fmt.Sprintf("%X", hasher.Sum32())
}

func localCopy(main string, remote string) (string, error) {
	//use the username embedded in "remote" to make a copy of the file locally
	//sibling to "main", <remote:username>.styl
	parsed, _ := url.Parse(remote)
	pathHash := hashed(parsed.Path)
	user := (*(*parsed).User).Username()
	localFile := filepath.Dir(main) + "/" + user + "-" + pathHash + ".styl"
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
	cmd := (*exec.Command("stylus", file))
	stderr, err1 := cmd.StderrPipe()
	if err1 != nil {
		return "", err1
	}
	
	err := cmd.Start()
	if err != nil {
		return "", err
	}
	
	go io.Copy(os.Stderr, stderr)
	broke := cmd.Wait()
	
	if broke != nil {
		log.Println("compile broken.");
		return "", broke;
	} else {
		log.Println("successfully compiled.");
	}
	
	return cssFile, nil
}

func backupAndMount(remote string) error {
	mntPoint := extractMountPoint(remote)
	if isMounted(mntPoint) != true {
		mountErr := mount(mntPoint)
		if mountErr != nil {
			return mountErr
		}
		//backupOfRemote(remote)
	}
	return nil
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
	backupAndMount(remoteCopyName)
	attemptCopy(compiledFile, remoteCopyName)
	log.Println("uploaded")
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
	//flag.Parse()
	log.Println(watchedFN)
	watcher, werr := fsnotify.NewWatcher()

	if werr != nil {
		log.Fatal(werr)
	}

	start := make(chan bool)
	finished := fileProcessor(start, watchedFN)

	processing := false
	shouldStart := false

	func() {
		werr = watcher.Watch(watchedFN)
		if werr != nil {
			log.Fatal(werr)
		}

		for {
			select {
			case ev := <-watcher.Event:
				log.Println(ev)
				if ev.IsDelete() {
					watcher.RemoveWatch(watchedFN)
					werr = watcher.Watch(watchedFN)
					if werr != nil {
						log.Fatal(werr)
						break
					}
				} else {
					if processing {
						shouldStart = true
					} else {
						shouldStart = false
						time.Sleep(time.Duration(50) * time.Millisecond)
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
