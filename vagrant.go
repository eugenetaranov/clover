package main

import (
	"bytes"
	"errors"
	"fmt"
	"html/template"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/koding/vagrantutil"
	homedir "github.com/mitchellh/go-homedir"
)

type sshItems struct {
	Host         string
	User         string
	Port         int
	IdentityFile string
}

const vagrantTemplate = `Vagrant.configure(2) do |config|
{{ range .Nodes }}
  config.vm.define "{{ .Name }}" do |{{ .Name }}|
	{{ $name := .Name }}
    {{ .Name }}.vm.box = "{{ .Provider.Box }}"
    {{ .Name }}.vm.hostname = "{{ .Name }}"
	## network
	{{ range .Provider.Network.ForwardedPort -}}
	{{ $list := split . ":" -}}
	{{ if eq (len $list) 5 }}
	{{- $name }}.vm.network "forwarded_port", guest_ip: "{{ index $list 0}}", guest: {{ index $list 1}}, host_ip: "{{ index $list 2}}", host: {{ index $list 3}}, protocol: "{{ index $list 4 -}}"
	{{ end }}
	{{ if eq (len $list) 3 }}
	{{- $name }}.vm.network "forwarded_port", guest_ip: "127.0.0.1", guest: {{ index $list 0}}, host_ip: "127.0.0.1", host: {{ index $list 1}}, protocol: "{{ index $list 2 -}}"
	{{ end }}
	{{- end }}
	## synced folders
	{{ $name }}.vm.synced_folder ".", "/vagrant", disabled: true
	{{ range .Provider.SyncedFolders -}}
	{{ $list := resolveDir . }}
	{{ $name }}.vm.synced_folder "{{ index $list 0}}", "{{ index $list 1}}"
	{{ end }}
	## shell
	{{ if eq .Provisioner.Name "ansible-local" -}}
	{{ $name }}.vm.provision "shell", path: "ansible.sh"
	{{ if .Provisioner.Shell -}}
	{{ $name }}.vm.provision "shell", path: "{{ $name }}.sh"
	{{ end }}
	{{ if .Provisioner.Playbook -}}
	{{ $name }}.vm.provision "shell", inline: "ansible-playbook {{ .Provisioner.Playbook }}"
	{{ end }}
	{{ end }}
  end
{{ end }}
end`

func resolveDir(dirPath string) (dirs []string, err error) {
	dirList := strings.Split(dirPath, ":")
	if len(dirList) != 2 {
		err = errors.New("Cannot parse shared dir")
		return
	}
	for _, dir := range dirList {
		if strings.HasPrefix(dir, "~/") {
			dirExpanded, err := homedir.Expand(dir)
			if err != nil {
				return nil, err
			}
			dirs = append(dirs, dirExpanded)
		} else {
			dirAbs, err := filepath.Abs(dir)
			if err != nil {
				return nil, err
			}
			dirs = append(dirs, dirAbs)
		}
	}
	return
}

func generateVagrantConfig(configFile string) (vagrantConfig string, err error) {
	conf, err := getConf(configFile)
	if err != nil {
		fmt.Println("Error:", err)
		os.Exit(1)
	}
	var tpl bytes.Buffer
	funcMap := template.FuncMap{
		"split":      strings.Split,
		"resolveDir": resolveDir,
	}
	vc, err := template.New("vagrant").Funcs(funcMap).Parse(vagrantTemplate)
	if err != nil {
		fmt.Println("Error:", err)
	}
	err = vc.Execute(&tpl, conf)
	vagrantConfig = tpl.String()
	return
}

func getVagrantDir(configFile string) (vagrantDir string, err error) {
	re := regexp.MustCompile(`\.?([a-zA-Z0-9\-]+)\.yml`)
	parsed := re.FindStringSubmatch(configFile)
	if len(parsed) != 2 {
		err = errors.New("configuration file must have yml extension")
	} else {
		vagrantDir = fmt.Sprintf(".%s", parsed[1])
	}
	return
}

func convergeVagrant(node *nodeType, configFile string) (err error) {
	var vagrantConfig string
	// if Vagrantfile does not exist, generate one and any shell script defined
	if _, err = os.Stat(fmt.Sprintf("%s/Vagrantfile", vagrantDir)); os.IsNotExist(err) {
		vagrantConfig, err = generateVagrantConfig(configFile)
		if err != nil {
			return
		}

	}

	vagrantDir, err = getVagrantDir(configFile)
	if err != nil {
		return
	}

	vagrant, err := vagrantutil.NewVagrant(vagrantDir)
	if err != nil {
		return
	}

	err = vagrant.Create(vagrantConfig)
	if err != nil {
		return
	}

	// generate shell provisioner script, if any
	if node.Provisioner.Shell != "" {
		if _, err = os.Stat(fmt.Sprintf("%s/%s.sh", vagrantDir, node.Name)); os.IsNotExist(err) {
			os.Chdir(vagrantDir)
			err = writeFile(fmt.Sprintf("%s.sh", node.Name), node.Provisioner.Shell)
			if err != nil {
				return
			}
			os.Chdir("..")

		}
	}

	// generate ansible installation script for ansible-local provider
	if node.Provisioner.Name == "ansible-local" {
		if _, err = os.Stat(fmt.Sprintf("%s/ansible.sh", vagrantDir)); os.IsNotExist(err) {
			os.Chdir(vagrantDir)

			if err = writeFile("ansible.sh", ansiblesh); err != nil {
				fmt.Println("Error:", err)
			}
			os.Chdir("..")
		}
	}

	// run vagrant up if not created, provision if it is running
	status, _ := vagrant.Status()
	if status.String() == "NotCreated" {
		output, err := vagrant.Up()

		if err != nil {
			return err
		}

		for line := range output {
			log.Println(line.Line)
		}

	} else if status.String() == "Running" {
		os.Chdir(vagrantDir)

		cmd := exec.Command("vagrant", "provision")
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		err = cmd.Run()
		if err != nil {
			fmt.Println("Error:", err)
			os.Exit(1)
		}
		os.Chdir("..")
	} else {
		fmt.Printf("State is %s, better destroy and run converge again", status)
		os.Exit(1)
	}

	if node.Provisioner.Name == "ansible" {
		if err = generateAnsibleHosts(node, vagrantDir); err != nil {
			return
		}

		if err = execInstalled("ansible-playbook", "--version"); err != nil {
			return
		}

		fmt.Printf("Provisioning %s node with ansible:\n", node.Name)

		var cmd *exec.Cmd
		argsRaw := []string{"-i", fmt.Sprintf("%s/ansiblehosts_%s", vagrantDir, node.Name), node.Provisioner.Playbook}
		if len(node.Provisioner.Extravars) > 0 {
			var extraVars []string
			for _, i := range node.Provisioner.Extravars {
				extraVars = append(extraVars, "--extra-vars")
				extraVars = append(extraVars, i)
			}
			argsRaw = append(argsRaw, extraVars...)
		}

		fmt.Println("    ", "ansible-playbook", strings.Join(argsRaw, " "))
		cmd = exec.Command("ansible-playbook", argsRaw...)

		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		err = cmd.Run()
	}
	return
}

func sshVagrant(vmName string, configFile string) (err error) {
	vagrantDir, err = getVagrantDir(configFile)
	if err != nil {
		return
	}
	os.Chdir(vagrantDir)
	defer os.Chdir("..")
	cmd := exec.Command("vagrant", "ssh", vmName)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err = cmd.Run()
	return
}

func getVagrantSSHDetails(vagrantDir string, vmName string) (sshConn sshItems, err error) {
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
			sshConn.Host = strings.Split(item, " ")[1]
		} else if strings.HasPrefix(item, "User ") {
			sshConn.User = strings.Split(item, " ")[1]
		} else if strings.HasPrefix(item, "Port ") {
			sshConn.Port, err = strconv.Atoi(strings.Split(item, " ")[1])
			if err != nil {
				return
			}
		} else if strings.HasPrefix(item, "IdentityFile ") {
			sshConn.IdentityFile = strings.Split(item, " ")[1]
		}
	}
	return
}
