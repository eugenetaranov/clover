package main

import (
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"regexp"

	"github.com/docopt/docopt-go"
	"github.com/koding/vagrantutil"
	yaml "gopkg.in/yaml.v2"
)

type configType struct {
	Nodes []nodeType `yaml:"nodes"`
}

type Provisioner struct {
	Name      string   `yaml:"name"`
	Playbook  string   `yaml:"playbook"`
	Content   string   `yaml:"content"`
	RunOnce   bool     `yaml:"run_once"`
	Groups    []string `yaml:"groups"`
	Extravars []string `yaml:"extra_vars"`
}

type nodeType struct {
	Name     string `yaml:"name"`
	Provider struct {
		Name          string   `yaml:"name"`
		Box           string   `yaml:"box"`
		SyncedFolders []string `yaml:"synced_folders"`
		Network       struct {
			ForwardedPort []string `yaml:"forwarded_port"`
		} `yaml:"network"`
	} `yaml:"provider"`
	Provisioner []Provisioner `yaml:"provisioner"`
	Verifier    struct {
		Name     string `yaml:"name"`
		GossFile string `yaml:"goss_file"`
	} `yaml:"verifier"`
	SSH struct {
		Host         string
		User         string
		Port         int
		IdentityFile string
	}
}

func (c *configType) Parse(data []byte) error {
	return yaml.Unmarshal(data, c)
}

var (
	conf       configType
	vagrantDir string
	configFile string
)

func writeFile(filename string, content string) (err error) {
	err = ioutil.WriteFile(filename, []byte(content), 0644)
	return
}

// reads and parses config file
func getConf(configFile string) (config configType, err error) {
	data, err := ioutil.ReadFile(configFile)
	if err != nil {
		return
	}
	config.Parse(data)
	return
}

func getNodeConf(conf *configType, vmName string) (node nodeType, err error) {
	for _, node := range conf.Nodes {
		if vmName == node.Name {
			return node, nil
		}
	}
	err = fmt.Errorf("Configuration for node %s was not found", vmName)
	return
}

func execInstalled(executable string, params string) (err error) {
	_, err = exec.LookPath(executable)
	return
}

func getConfigFileNoExt(configFile string) (ConfigFileNoExt string, err error) {
	re := regexp.MustCompile(`(\.?[a-zA-Z0-9\-]+)\.yml`)
	parsed := re.FindStringSubmatch(configFile)
	if len(parsed) != 2 {
		err = errors.New("configuration file must have yml extension")
	} else {
		ConfigFileNoExt = parsed[1]
	}
	return
}

func converge(node *nodeType, configFile string) (err error) {
	if node.Provider.Name == "vagrant" {
		if err = convergeVagrant(node, configFile); err != nil {
			return
		}
	} else {
		err = fmt.Errorf("provider %s for node %s is not supported", node.Provider.Name, node.Name)
	}
	return
}

func sshNode1(node *nodeType, vmName string, configFile string) (err error) {
	if node.Provider.Name == "vagrant" {
		if err = sshVagrant(vmName, configFile); err != nil {
			return
		}
	} else {
		err = fmt.Errorf("provider %s for node %s is not supported", node.Provider.Name, node.Name)
	}
	return

}

func main() {
	usage := `
usage: [-h] <command> [<config> <vm_name>]

commands:
    converge            bootstraps virtual machine and applies playbook
    destroy             destroys virtual machine
    status              checks status of virtual machine
    verify              runs one of the verifiers against the virtual machine
	ssh                 ssh into virtual machine`

	arguments, _ := docopt.ParseDoc(usage)
	command := arguments["<command>"]
	configFile := arguments["<config>"]
	vmName := arguments["<vm_name>"]

	if configFile == nil {
		configFile = "clover.yml"
	}

	conf, err := getConf(configFile.(string))
	if err != nil {
		fmt.Println("Error:", err)
		os.Exit(1)
	}
	vagrantDir, err := getVagrantDir(configFile.(string))
	if err != nil {
		fmt.Println("Error:", err)
		os.Exit(1)
	}

	err = execInstalled("vagrant", "version")
	if err != nil {
		fmt.Println("Error:", err)
		os.Exit(1)
	}

	if command == "converge" {

		if vmName != nil {
			node, err := getNodeConf(&conf, vmName.(string))
			if err != nil {
				fmt.Println("Error:", err)
				os.Exit(1)
			}
			if err = converge(&node, configFile.(string)); err != nil {
				fmt.Println("Error:", err)
				os.Exit(1)
			}
		} else {
			for _, node := range conf.Nodes {
				if err = converge(&node, configFile.(string)); err != nil {
					fmt.Println("Error:", err)
					os.Exit(1)
				}
				fmt.Println("*** Converged node", node.Name)
			}
		}
	}

	if command == "status" {
		vagrant, _ := vagrantutil.NewVagrant(vagrantDir)

		status, err := vagrant.Status()
		if err != nil {
			fmt.Println("Error:", err)
			os.Exit(1)
		}

		fmt.Println(status)
	}

	if command == "destroy" {
		vagrant, _ := vagrantutil.NewVagrant(vagrantDir)

		if status, _ := vagrant.Status(); status != vagrantutil.Running {
			fmt.Println("Error: vm is not running, current status:", status)
			os.Exit(1)
		}
		output, _ := vagrant.Destroy()
		for line := range output {
			log.Println(line.Line)
		}
		if err := os.RemoveAll(vagrantDir); err != nil {
			fmt.Println("Failed to remove", vagrantDir)
			os.Exit(1)
		}
		fmt.Println("Successfully destroyed")
	}

	if command == "ssh" {
		if vmName == nil {
			fmt.Println("Error: vmname is required")
			os.Exit(1)
		}

		node, err := getNodeConf(&conf, vmName.(string))
		if err != nil {
			fmt.Println("Error:", err)
			os.Exit(1)
		}

		err = sshNode1(&node, vmName.(string), configFile.(string))
		if err != nil {
			fmt.Println("Error:", err)
			os.Exit(1)
		}
	}

	if command == "verify" {

		if vmName != nil {
			fmt.Println("Verifying node", vmName)
			node, err := getNodeConf(&conf, vmName.(string))
			if err != nil {
				fmt.Println("Error:", err)
				os.Exit(1)
			}
			if err = node.verify(vagrantDir); err != nil {
				fmt.Println("Error:", err)
				os.Exit(1)
			}
		} else {
			for _, node := range conf.Nodes {
				fmt.Println("Verifying node", node.Name)
				if err = node.verify(vagrantDir); err != nil {
					fmt.Println("Error:", err)
					os.Exit(1)
				}
				fmt.Println("*** Verified node", node.Name)
			}
		}
	}

}
