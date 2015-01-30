package main

//potential improvements:
//backing up the styl files after modification
//unmount the mount point after 1 hour of inactivity

import (
	_"flag"
	"errors"
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
	if _, err := os.Stat(r); os.IsNotExist(err) {
		return r
	}
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
	//cmd := fmt.Sprintf("bash -c 'ex=`gvfs-mount -l|grep \"%s\"|wc -l;exit $((1 - $ex))`'", softDevice)
	out, _ := (*exec.Command("gvfs-mount", "-l")).Output()
	return strings.Contains(string(out), softDevice)
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
	parsed, _ := url.Parse(remote)
	pathHash := hashed(parsed.Path)
	localFile := deriveName(localName(main, remote), "", "-" + pathHash, filepath.Ext(main))
	err := (*exec.Command("cp", main, localFile)).Run()
	if err != nil {
		return "", err
	}
	return localFile, nil
}

func deriveName(file, prefix, suffix, newExt string) string {
	base := filepath.Base(file)
	ext := filepath.Ext(file)
	re, _ := regexp.Compile("(.*)" + ext + "$");
	newFn := re.FindStringSubmatch(base)[1]
	return prefix + newFn + suffix + newExt;
}

func compile(file string) (string, error) {
	//run stylus against the file
	cssFile := deriveName(file, "", "", ".css");
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
	}
	
	log.Println("successfully compiled.");
	return cssFile, nil
}

func localName(main, remote string) string {
	if _, err := os.Stat(remote); os.IsNotExist(err) {
		return remote
	}
	parsed, _ := url.Parse(remote)
	user := (*(*parsed).User).Username()
	return filepath.Dir(main) + "/" + user + ".styl"
}

func createComparableBackup(remote, where string) error {
	url, _ := url.Parse(remote)
	fn := (* url).Path
	baseName := localName(where + "/.", remote)
	pathHash := hashed(fn)
	fn = where + "/" + deriveName(baseName, "", "-" + pathHash + "-upstream", filepath.Ext(fn))
	return attemptCopy(remote, fn)
}

func backupAndMount(remote, where string) error {
	mntPoint := extractMountPoint(remote)
	if isMounted(mntPoint) != true {
		log.Println("not mounted")
		mountErr := mount(mntPoint)
		if mountErr != nil {
			return mountErr
		}
		return createComparableBackup(remote, where)
	}
	log.Println("mounted")
	return nil
}

func differences(f1, f2 string) (string, bool) {
	cmd := (*exec.Command("diff", "-C", "5", f1, f2))
	out, _ := cmd.Output()
	diff := strings.TrimSpace(string(out))
	return diff, diff != ""
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
	backupDir, _ := filepath.Abs(filepath.Dir(localFN))
	backupAndMount(remoteCopyName, backupDir)
	backupFile := deriveName(compiledFile, "", "-upstream", ".css")
	if _, ex := os.Stat(backupFile); os.IsNotExist(ex) {
		attemptCopy(compiledFile, remoteCopyName)
		os.Remove(compiledFile)
		log.Println("uploaded")
		return nil
	}
	diff, isDifferent := differences(compiledFile, backupFile);
	if isDifferent == false {
		attemptCopy(compiledFile, remoteCopyName)
		log.Println("uploaded")
		os.Remove(compiledFile)
		os.Remove(backupFile)
		return nil
	}
	log.Println("There was an error: an upstream file exists, with differences.")
	fmt.Println(diff)
	log.Println(backupFile)
	log.Println("Please review and delete the file to continue")
	return errors.New("Upsteam backup detected, please remove to continue. (" + backupFile + ")")
}

func fileProcessor(process chan bool, proxy string) chan bool {
	done := make(chan bool)
	go func() {
		for {
			select {
			case <-process:
				err := processFile(proxy)
				if err != nil {
					log.Println(err)
				}
				done <- true
			}
		}
	}()
	return done
}

func main() {
	//log.SetFlags(log.Ltime | log.Lshortfile)
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
