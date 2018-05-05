package main

import "fmt"

func (node *nodeType) verify(vagrantDir string) (err error) {
	if node.Verifier.Name == `goss` {
		err = node.sshCommand(vagrantDir, fmt.Sprintf("sudo /usr/bin/goss --gossfile %s validate", node.Verifier.GossFile), true)
		if err != nil {
			return
		}
	} else {
		err = fmt.Errorf("Unsupported verifier %s for node %s", node.Verifier.Name, node.Name)
	}
	return
}
