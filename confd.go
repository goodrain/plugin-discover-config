// RAINBOND, Application Management Platform
// Copyright (C) 2014-2017 Goodrain Co., Ltd.

// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version. For any non-GPL usage of Rainbond,
// one or multiple Commercial Licenses authorized by Goodrain Co., Ltd.
// must be obtained first.

// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU General Public License for more details.

// You should have received a copy of the GNU General Public License
// along with this program. If not, see <http://www.gnu.org/licenses/>.

package main

import (
	"crypto/md5"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"
	"errors"
	"strconv"
)

//Config config
type Config struct {
	BasePort []struct {
		InnerPort          int               `json:"port"`
		ListenPort         int               `json:"listen_port"`
		Protocol           string            `json:"protocol"`
		DependServiceID    string            `json:"depend_service_id"`
		DependServiceAlias string            `json:"depend_service_alias"`
		Options            map[string]string `json:"options"`
	} `json:"base_ports"`
	BaseService []struct {
		DependServiceID    string            `json:"depend_service_id"`
		DependServiceAlias string            `json:"depend_service_alias"`
		Port               int               `json:"port"`
		Protocol           string            `json:"protocol"`
		Options            map[string]string `json:"options"`
	} `json:"base_services"`
	BaseNormal struct {
		Options map[string]string `json:"options"`
	} `json:"base_normal"`
}

var (
	inFile   = flag.String("template", "nginx.conf.template", "read template")
	outFile  = flag.String("out", "nginx.conf", "write to file")
	script   = flag.String("shell", "callback.sh", "exec script")
	interval = flag.Int("interval", 10, "discover run TTL")
)

func main() {
	log.SetOutput(os.Stdout)
	flag.Parse()

	discoverURL := os.Getenv("DISCOVER_URL")
	if discoverURL == "" {
		log.Println("Discover url is empty,exist.")
		os.Exit(1)
	}

	log.Println("the confd is started.")

	closer := make(chan struct{}, 1)
	defer close(closer)
	timer := time.NewTimer(time.Second * time.Duration(*interval))
	go func() {
		for {
			discover(discoverURL, call)
			select {
			case <-timer.C:
				timer.Reset(time.Second * time.Duration(*interval))
			case <-closer:
				return
			}
		}
	}()

	//step finally: listen Signal
	term := make(chan os.Signal)
	signal.Notify(term, os.Interrupt, syscall.SIGTERM)
	select {
	case s := <-term:
		close(term)
		log.Printf("Received a Signal %s, exiting gracefully...", s.String())
	}
	log.Printf("See you next time!")
}

var lasthash string

func call(data []byte) error {
	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		log.Println("Decode discover res body error:", err)
		return err
	}

	isChange := isChange(data)
	if !isChange {
		return nil
	}

	log.Printf("update plugin data: %+v", config)

	if len(config.BasePort) < 1 {
		err := errors.New("the config.BasePort length is 0")
		log.Println(err)
		return err
	}

	if *inFile != "" {
		return writeFile(config, *inFile)
	}

	return execShell(string(data), *script)
	return nil
}

func isChange(data []byte) bool {
	hash := md5.New()
	re := hash.Sum(data)
	if lasthash == string(re) {
		return false
	}
	lasthash = string(re)
	return true
}

func writeFile(config Config, templateFile string) error {
	c, err := ioutil.ReadFile(templateFile)
	if err != nil {
		return err
	}
	content := string(c)

	content = strings.Replace(content, "__listen_port__", strconv.Itoa(config.BasePort[0].ListenPort), -1)

	innerIP, ok := config.BasePort[0].Options["inner_ip"]
	if !ok {
		innerIP = "127.0.0.1"
	}
	news := fmt.Sprintf("%s://%s:%s", config.BasePort[0].Protocol, innerIP, strconv.Itoa(config.BasePort[0].InnerPort))
	content = strings.Replace(content, "__backend__", news, -1)

	if err := ioutil.WriteFile(*outFile, []byte(content), 0644); err != nil {
		return err
	}

	exec.Command("nginx", "-s", "reload").Start()

	return nil
}

func execShell(sendStdin, script string) error {
	if _, err := os.Stat(script); err != nil {
		log.Println("on update script shell not exist.", err)
		return err
	}
	log.Printf("execing: %v with stdin: %v", script, sendStdin)
	// TODO: Switch to sending stdin from go
	out, err := exec.Command("bash", "-c", fmt.Sprintf("echo -e '%v' | %v", sendStdin, script)).CombinedOutput()
	if err != nil {
		log.Fatalf("Failed to execute %v: %v, err: %v", script, string(out), err)
		return err
	}
	log.Print(string(out))

	return nil
}

//discover discover
func discover(discoverURL string, call func([]byte) error) {

	req, err := http.NewRequest("GET", discoverURL, nil)
	if err != nil {
		log.Println("new discover req error:", err)
		return
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Println("do discover req error", err)
		return
	}
	if res.Body != nil {
		defer res.Body.Close()
		data, err := ioutil.ReadAll(res.Body)
		if err != nil {
			log.Println("read discover res body error:", err)
			return
		}
		call(data)
	}
}

