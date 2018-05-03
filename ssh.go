package main

import (
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"golang.org/x/crypto/ssh"
)

func getVagrantSSHDetails2(node *nodeType, vagrantDir string, vmName string) (err error) {
	os.Chdir(vagrantDir)
	defer os.Chdir("..")
	out, err := exec.Command("vagrant", "ssh-config", vmName).Output()
	if err != nil {
		return
	}
	outSplitted := strings.Split(string(out), "\n")

	for _, item := range outSplitted {
		item = strings.TrimSpace(item)
		if strings.HasPrefix(item, "HostName ") {
			node.SSH.Host = strings.Split(item, " ")[1]
		} else if strings.HasPrefix(item, "User ") {
			node.SSH.User = strings.Split(item, " ")[1]
		} else if strings.HasPrefix(item, "Port ") {
			node.SSH.Port, err = strconv.Atoi(strings.Split(item, " ")[1])
			if err != nil {
				return
			}
		} else if strings.HasPrefix(item, "IdentityFile ") {
			node.SSH.IdentityFile = strings.Split(item, " ")[1]
		}
	}
	return
}

func (node *nodeType) sshNode(vagrantDir string, cmd string, output bool) (err error) {
	getVagrantSSHDetails2(node, vagrantDir, node.Name)

	key, err := ioutil.ReadFile(node.SSH.IdentityFile)
	if err != nil {
		return
	}

	signer, err := ssh.ParsePrivateKey(key)
	if err != nil {
		return
	}

	config := &ssh.ClientConfig{
		User: node.SSH.User,
		Auth: []ssh.AuthMethod{
			ssh.PublicKeys(signer),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}

	client, err := ssh.Dial("tcp", fmt.Sprintf("%s:%s", node.SSH.Host, strconv.Itoa(node.SSH.Port)), config)
	if err != nil {
		return
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		return
	}
	defer session.Close()

	stderr, _ := session.StderrPipe()
	stdout, _ := session.StdoutPipe()

	go io.Copy(os.Stderr, stderr)
	go io.Copy(os.Stdout, stdout)
	out, err := session.Output(cmd)
	if err != nil {
		return
	}

	if output {
		fmt.Println(string(out))
	}
	return
}
