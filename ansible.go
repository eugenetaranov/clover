package main

import (
	"bytes"
	"fmt"
	"html/template"
	"os"
)

const ansiblesh = `
apt && apt update && apt install -y ansible
yum && yum install -y ansible
`

const ansibleHostsTemplate = `{{ $ssh := .SSH }}
default ansible_host={{ $ssh.Host }} ansible_user={{ $ssh.User }} ansible_port={{ $ssh.Port }} ansible_ssh_private_key_file={{ $ssh.IdentityFile }}
{{ if .Groups }}
{{ range .Groups -}}
[{{ . }}]
default ansible_host={{ $ssh.Host }} ansible_user={{ $ssh.User }} ansible_port={{ $ssh.Port }} ansible_ssh_private_key_file={{ $ssh.IdentityFile }}
{{ end }}
{{ end }}
`

type ansibleHost struct {
	SSH    sshItems
	Groups []string
}

// generates ansible hosts file for node <vagrantdir>/ansiblehosts_<nodename>
func generateAnsibleHosts(nodeName string, provisioner Provisioner, vagrantDir string) (err error) {
	if _, statErr := os.Stat(fmt.Sprintf("%s/ansiblehosts_%s", vagrantDir, nodeName)); os.IsNotExist(statErr) {
		sshConn, _ := getVagrantSSHDetails(vagrantDir, nodeName)
		if err != nil {
			return err
		}

		var ansibleHost ansibleHost
		ansibleHost.SSH = sshConn
		ansibleHost.Groups = provisioner.Groups

		var tpl bytes.Buffer
		vc, err := template.New("hosts").Parse(ansibleHostsTemplate)
		if err != nil {
			fmt.Println("Error:", err)
		}
		err = vc.Execute(&tpl, ansibleHost)
		ansibleHostsFileContent := tpl.String()

		os.Chdir(vagrantDir)
		if err := writeFile(fmt.Sprintf("ansiblehosts_%s", nodeName), ansibleHostsFileContent); err != nil {
			return err
		}
		os.Chdir("..")
	}
	return
}
