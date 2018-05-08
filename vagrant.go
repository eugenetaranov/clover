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
	{{ $name }}.vm.synced_folder "{{ $name }}", "/clover"
	{{ range .Provider.SyncedFolders -}}
	{{ $list := resolveDir . }}
	{{ $name }}.vm.synced_folder "{{ index $list 0}}", "{{ index $list 1}}"
	{{ end }}
	## shell
	{{ range .Provisioner }}
	{{ if eq .Name "ansible-local" -}}
	{{ $name }}.vm.provision "shell", path: "ansible.sh"
	{{ if .Playbook -}}
	{{ $name }}.vm.provision "shell", inline: "ansible-playbook {{ .Playbook }}"
	{{ end }}
	{{ end }}
	{{ end }}
  end
{{ end }}
end`

// converts "<hostdir>:<vmdir>" string into slice of strings with absolute paths
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

	// .<vagrantDir>/<node.Name>
	nodeDir := filepath.Join(vagrantDir, node.Name)
	if _, statErr := os.Stat(nodeDir); os.IsNotExist(statErr) {
		err = os.Mkdir(nodeDir, 0755)
		if err != nil {
			return
		}
	}

	// install stage tasks
	for _, provisioner := range node.Provisioner {

		// generate ansible installation script for ansible-local provider before vagrant up
		if provisioner.Name == "ansible-local" {
			if _, err = os.Stat(filepath.Join(nodeDir, "ansible.sh")); os.IsNotExist(err) {
				os.Chdir(nodeDir)

				if err = writeFile("ansible.sh", ansiblesh); err != nil {
					fmt.Println("Error:", err)
				}
				os.Chdir("../..")
			}
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
			return
		}
		os.Chdir("..")
	} else {
		return fmt.Errorf("State is %s, destroy and run converge again", status)
	}

	sftpClient, err := node.sftpConn(vagrantDir)
	if err != nil {
		sftpClient.Close()
		return err
	}
	defer sftpClient.Close()

	// create tmp dir
	if _, err = sftpClient.Lstat(".clover"); os.IsNotExist(err) {
		if err = sftpClient.Mkdir(".clover"); err != nil {
			return
		}
	}

	// uploading files
	for _, file := range node.Files {

		// check if file exists
		if _, err = sftpClient.Lstat(file.Path); os.IsNotExist(err) {

			// upload file into temporary location
			tmpFileName := randFileName()
			f, err := sftpClient.Create(sftpClient.Join(".clover", tmpFileName))
			if err != nil {
				return err
			}
			if _, err := f.Write([]byte(file.Content)); err != nil {
				return err
			}
			f.Close()

			// create dir if not exists
			if _, err = sftpClient.Lstat(filepath.Dir(file.Path)); os.IsNotExist(err) {
				if err = node.sshCommand(vagrantDir, fmt.Sprintf("sudo mkdir -p %s", filepath.Dir(file.Path)), false); err != nil {
					return err
				}
			}

			// move temprorary file into destination
			if err = node.sshCommand(vagrantDir, fmt.Sprintf("sudo mv %s %s", sftpClient.Join(".clover", tmpFileName), file.Path), false); err != nil {
				return err
			}

			// reset owner and group
			if file.User != "" && file.Group != "" {
				if err = node.sshCommand(vagrantDir, fmt.Sprintf("sudo chown %s:%s %s", file.User, file.Group, file.Path), false); err != nil {
					return err
				}
			}

			// reset mode
			if file.Mode != 0 {
				if err = node.sshCommand(vagrantDir, fmt.Sprintf("sudo chmod %d %s", file.Mode, file.Path), false); err != nil {
					return err
				}

			}

		}
	}

	// converge stage tasks
	for i, provisioner := range node.Provisioner {

		// ansible provisioner
		if provisioner.Name == "ansible" {
			if err = execInstalled("ansible-playbook", "--version"); err != nil {
				return
			}

			if err = generateAnsibleHosts(node.Name, provisioner, vagrantDir); err != nil {
				return
			}

			fmt.Printf("Provisioning %s node with ansible:\n", node.Name)

			var cmd *exec.Cmd
			argsRaw := []string{"-i", fmt.Sprintf("%s/ansiblehosts_%s", vagrantDir, node.Name), provisioner.Playbook}
			if len(provisioner.Extravars) > 0 {
				var extraVars []string
				for _, i := range provisioner.Extravars {
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
			if err != nil {
				return
			}
		}

		// shell provisioners
		if provisioner.Name == "shell" {

			// put sh script into .<vagrantDir>/<nodeName> which is mounted into
			if node.Provider.Name == "vagrant" {
				if _, err = os.Stat(filepath.Join(nodeDir, fmt.Sprintf("%d.sh", i))); os.IsNotExist(err) {
					os.Chdir(nodeDir)
					err = writeFile(fmt.Sprintf("%d.sh", i), provisioner.Content)
					if err != nil {
						return
					}
					os.Chdir("../..")
				}
			}

			// generate shell script
			f, err := sftpClient.Create(sftpClient.Join(".clover", fmt.Sprintf("%d.sh", i)))
			if err != nil {
				return err
			}
			if _, err := f.Write([]byte(provisioner.Content)); err != nil {
				return err
			}
			f.Close()

			node.sshCommand(vagrantDir, "sudo bash "+filepath.Join(".clover", fmt.Sprintf("%d.sh", i)), true)
		}
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
