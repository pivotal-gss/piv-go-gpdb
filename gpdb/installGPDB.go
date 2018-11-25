package main

import (
	"fmt"
	"os"
	"strings"
)

// Get and generate host file if doesn't exists the hostname
func (i *Installation) generateHostFile() {

	i.HostFileLocation = fmt.Sprintf("%s/hostfile", os.Getenv("HOME"))
	Debugf("Generating the hostfile at %s", i.HostFileLocation)

	exists, _ := doesFileOrDirExists(i.HostFileLocation)
	if !exists {
		Infof("Host file doesn't exists, creating one: %s", i.HostFileLocation)
		// Read the contents from the /etc/hosts and generate a hostfile.
		hosts := contentExtractor(readFile("/etc/hosts"), "{if (NR!=1) {print $2}}", []string{})
		s := removeBlanks(hosts.String())
		// write to the file
		writeFile(i.HostFileLocation, []string{s})
	} else {
		Infof("Found host file at location: %s", i.HostFileLocation)
	}
}

// Check if the directory provided is readable and writeable
func dirValidator() error {

	Debugf("Checking for the existences of master and segment directory")

	// Check if the master & segment location is readable and writable
	masterDirExists, err := doesFileOrDirExists(Config.INSTALL.MASTERDATADIRECTORY)
	if err != nil {
		Fatalf("Cannot get the information of directory %s, err: %v", Config.INSTALL.MASTERDATADIRECTORY, err)
	}

	segmentDirExists, err := doesFileOrDirExists(Config.INSTALL.SEGMENTDATADIRECTORY)
	if err != nil {
		Fatalf("Cannot get the information of directory %s, err: %v", Config.INSTALL.SEGMENTDATADIRECTORY, err)
	}

	// if the file doesn't exists then let try creating it ...
	if !masterDirExists || !segmentDirExists {
		CreateDir(Config.INSTALL.MASTERDATADIRECTORY)
		CreateDir(Config.INSTALL.SEGMENTDATADIRECTORY)
	}

	return nil
}


// Check if the binaries exits and unzip the binaries.
func (i *Installation) getBinaryFile() string {
	Debugf("Finding and unziping the binaries for the version %s", cmdOptions.Version)
	return unzip(fmt.Sprintf("*%s*", cmdOptions.Version))
}

// Installing the greenplum binaries
func (i *Installation) installProduct() {
	// Installing the binaries.
	binFile := i.getBinaryFile()

	// Location and name of the binaries
	Infof("Using the bin file to install the GPDB Product: %s", binFile)
	i.BinaryInstallationLocation = "/usr/local/greenplum-db-" + cmdOptions.Version

	// Execute the command to install the binaries
	var scriptOption = []string{"yes", i.BinaryInstallationLocation, "yes", "yes"}
	err := executeBinaries(binFile, "install_software.sh", scriptOption)
	if err != nil {
		Fatalf("Failed in installing the binaries, err: %v", err)
	}

	// Now source the environment if installation went fine
	err = sourceGPDBPath(i.BinaryInstallationLocation)
	if err != nil {
		Fatalf("Failed to set the environment variable while sourcing the file")
	}
}

// Check if the provided hostnames are valid
func (i *Installation) setUpHost() {

	Infof("Setting up & Checking if the host is reachable")

	// Get the hostname of the master
	i.GPInitSystem.MasterHostname = os.Getenv("HOSTNAME")
	if i.GPInitSystem.MasterHostname == "" {
		Fatalf("The environment variable 'HOSTNAME' for master host is not set")
	}

	// Check is host is reachable
	i.isHostReachable()

	// Enable passwordless login
	i.executeGpsshExkey()

	// If this is a multi installation then install the software on all the segment host
	if i.SingleORMulti == "multi" {
		i.runSegInstall()
	}
}

// Check if all the host are working from the hostfile
func (i *Installation) isHostReachable() {

	// Get all the hostname from the hostfile and check if the host if reachable
	var saveReachableHost []string
	var segmentHost []string
	for _, host := range strings.Split(string(readFile(i.HostFileLocation)), "\n"){
		if host != "" && checkHostReachability(fmt.Sprintf("%s:22", host), true) {
			Debugf("The host %s is reachable", host)
			saveReachableHost = append(saveReachableHost, host)
			if host != i.GPInitSystem.MasterHostname {
				segmentHost = append(segmentHost, host)
			}
		}
	}

	// Save the reachable host
	Debugf("Total Host reachable is: %d", len(saveReachableHost))
	if len(saveReachableHost) == 0 {
		Fatalf("No hosts are reachable from the hostfile %s, check the host", i.HostFileLocation)
	} else if len(saveReachableHost) == 1 && saveReachableHost[0] == i.GPInitSystem.MasterHostname {
		i.SingleORMulti = "single"
	} else if len(saveReachableHost) == 1 && saveReachableHost[0] != i.GPInitSystem.MasterHostname {
		Fatalf("Master hosts are not reachable from the hostfile %s, check the host", i.HostFileLocation)
	}

	// Check if we have equal number of hosts
	if i.SingleORMulti == "multi" && len(segmentHost)%2 == 1 {
		Fatalf("There is odd number of segment host, installation cannot continue")
	} else if i.SingleORMulti == "multi" &&  len(segmentHost) == 0 {
		Fatalf("No segment host found, cannot continue")
	}

	// Save the working host on a separate file
	i.WorkingHostFileLocation = Config.CORE.TEMPDIR + "hostfile"
	deleteFile(i.WorkingHostFileLocation)
	writeFile(i.WorkingHostFileLocation, saveReachableHost)

	// Save segment host list
	i.SegmentHostLocation = Config.CORE.TEMPDIR + "hostfile_segment"
	deleteFile(i.SegmentHostLocation)
	writeFile(i.SegmentHostLocation, segmentHost)
}

// Run keyless access to the server
func (i *Installation) executeGpsshExkey() {
	Infof("Running gpssh-exkeys to enable keyless access on this server")
	executeOsCommand(fmt.Sprintf("%s/bin/gpssh-exkeys", os.Getenv("GPHOME")), "-f", i.WorkingHostFileLocation)
}


// Run the gpseginstall on all host
func (i *Installation) runSegInstall() {
	Infof("Running seg install to install the software on all the host")
	executeOsCommand(fmt.Sprintf("%s/bin/gpseginstall", os.Getenv("GPHOME")), "-f", i.SegmentHostLocation)
	i.createSoftLink()
}

// On the newer version of the GPDB they need the soft link for
// database to work
func (i *Installation) createSoftLink() {
	Debugf("Creating the softlink for the binaries on all the host")
	contents := readFile(i.SegmentHostLocation)
	for _, v := range strings.Split(removeBlanks(string(contents)), "\n") {
		softLinkFile := Config.CORE.TEMPDIR + "soft_link.sh"
		createFile(softLinkFile)
		writeFile(softLinkFile, []string{
			fmt.Sprintf("ssh %s \"rm -rf /usr/local/greenplum-db\"", v),
			fmt.Sprintf("ssh %s \"ln -s %s /usr/local/greenplum-db\"", v, i.BinaryInstallationLocation),
		})
		executeOsCommand("/bin/sh", softLinkFile)
		deleteFile(softLinkFile)
	}
}

// Source greenplum path
func sourceGPDBPath(binLoc string) error {

	Debugf("Sourcing the greenplum binary location")

	// Setting up greenplum path
	err := os.Setenv("GPHOME", binLoc)
	if err != nil {
		return err
	}
	err = os.Setenv("PYTHONPATH", os.Getenv("GPHOME") + "/lib/python")
	if err != nil {
		return err
	}
	err = os.Setenv("PYTHONHOME", os.Getenv("GPHOME") + "/ext/python")
	if err != nil {
		return err
	}
	err = os.Setenv("PATH", os.Getenv("GPHOME") + "/bin:" + os.Getenv("PYTHONHOME") + "/bin:" + os.Getenv("PATH"))
	if err != nil {
		return err
	}
	err = os.Setenv("LD_LIBRARY_PATH", os.Getenv("GPHOME") + "/lib:" + os.Getenv("PYTHONHOME")+ "/lib:" + os.Getenv("LD_LIBRARY_PATH"))
	if err != nil {
		return err
	}
	err = os.Setenv("OPENSSL_CONF", os.Getenv("GPHOME") + "/etc/openssl.cnf")
	if err != nil {
		return err
	}

	return nil
}