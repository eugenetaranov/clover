---
Clover is a tool for testing infrastructure code on isolated platform.
---
It was heavily inspired by test-kitchen, just better :) - no ruby gems, allows multiple config files in the same directory, file sharing, custom shell provisioners.. Currently it supports [ansible](https://www.ansible.com) as provisioner and [goss](https://github.com/aelsabbahy/goss) as verifier.

### Dependencies
- vagrant

### Quick start
1. Run `git clone <clover repo>`, cd into clover
2. Install dependencies with `pip install -r requirements.txt`
2. `cd example`
3. Converge with `../clover converge`
4. Run tests with `../clover verify`

###### Commands
- `converge`: bootstraps virtual machine(s) and applies playbook
- `destroy`: destroys virtual machine(s)
- `status`: checks status of virtual machine(s)
- `verify`: runs one of the verifiers against the virtual machine(s)
- `ssh`: ssh into virtual machine

###### Options
- config: by default, it looks for .clover.yml in current directory but you can specify custom configuration file (with yml extentions or without)
- vm_name: by default, it converges, verifies, destroys all virtual machines specified in configuration file, this option allows to limit it to single virtual machine.

###### Examples
`clover converge`: converge all virtual machines defined in .clover.yml  
`clover converge openvpn.yml`: converge all virtual machines defined in openvpn.yml 
`clover verify openvpn.yml openvpnserver`: verify virtual machine *openvpnserver* defined in openvpn.yml  

#### Configuration file
`nodes` - may contain multiple virtual machines definitions;  
`nodes[].name` - required, virtual machine name, required  
`node[].provider` - required, provider section, applied during converge phase  
`node[].provider.name` - required, provider name, currently vagrant only  
`node[].provider.box` - required, vagrant box name, look [here](https://app.vagrantup.com/boxes/search) for more  
`node[].provider.synced_folders` - optional, list of local directories that are mounted into virtual machine, host path is separated with `:` from vm path. VM path must be absolute.  
`node[].provider.network` - optional, network settings go here  
`node[].provider.network.forwarded_port` - optional, list of host ports forwarded to vm ports, host port, vm port and protocol are separated by `:`  
`node[].provisioner[]` - required, provisioner section, applied during converge phase, list
`node[].provisioner[].name` - required, provisioner name, currently `ansible-local` only is supported  
`node[].provisioner[].playbook` - required, provisioner name, ansible playbook path, absolute inside th the virtual machine    
`node[].provisioner[].content` - optional, shell commands to be run during converge phase  
`node[].verifier` - optional, applied during verifier phase  
`node[].verifier.name` - optional, verifier's name, currently goss only
`node[].verifier.goss_file` - optional, absolute path to the goss file inside vm  


Example:
```
---
nodes:
  - name: web
    provider:
      name: vagrant
      box: hashicorp-vagrant/ubuntu-16.04
      synced_folders:
        - ansible:/ansible
    provisioner:
       - name: ansible
         playbook: ../web.yaml
         groups:
           - webservers
         extra_vars:
           - '@../envs/prod/group_vars/webservers/environment'
      - name: shell
        content: |
          #!/bin/bash
          test -f /usr/bin/goss || curl -L https://github.com/aelsabbahy/goss/releases/download/v0.3.5/goss-linux-amd64 -o /usr/bin/goss
          chmod +x /usr/bin/goss
    verifier:
      name: goss
      goss_file: /mnt/goss.yml

```
