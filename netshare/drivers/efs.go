package drivers

import (
	"fmt"
	log "github.com/Sirupsen/logrus"
	"github.com/calavera/dkvolume"
	"net"
	"os"
	"strings"
	"sync"
)

const (
	EfsTemplateURI = "%s.%s.efs.%s.amazonaws.com"
)

type efsDriver struct {
	root      string
	availzone string
	resolve   bool
	region    string
	mountm    *mountManager
	m         *sync.Mutex
	dnscache  map[string]string
}

func NewEFSDriver(root, az string, resolve bool) efsDriver {

	d := efsDriver{
		root:     root,
		resolve:  resolve,
		mountm:   NewVolumeManager(),
		m:        &sync.Mutex{},
		dnscache: map[string]string{},
	}
	md, err := fetchAWSMetaData()
	if err != nil {
		log.Fatalf("Error resolving AWS metadata: %s\n", err.Error())
		os.Exit(1)
	}
	d.region = md.Region
	if az == "" {
		d.availzone = md.AvailZone
	}
	return d
}

func (e efsDriver) Create(r dkvolume.Request) dkvolume.Response {
	return dkvolume.Response{}
}

func (e efsDriver) Remove(r dkvolume.Request) dkvolume.Response {
	log.Debugf("Removing volume %s\n", r.Name)
	return dkvolume.Response{}
}

func (e efsDriver) Path(r dkvolume.Request) dkvolume.Response {
	log.Debugf("Path for %s is at %s\n", r.Name, mountpoint(e.root, r.Name))
	return dkvolume.Response{Mountpoint: mountpoint(e.root, r.Name)}
}

func (e efsDriver) Mount(r dkvolume.Request) dkvolume.Response {
	e.m.Lock()
	defer e.m.Unlock()
	dest := mountpoint(e.root, r.Name)
	source := e.fixSource(r.Name)

	if e.mountm.HasMount(dest) && e.mountm.Count(dest) > 0 {
		log.Infof("Using existing EFS volume mount: %s\n", dest)
		e.mountm.Increment(dest)
		return dkvolume.Response{Mountpoint: dest}
	}

	log.Infof("Mounting EFS volume %s on %s\n", source, dest)

	if err := createDest(dest); err != nil {
		return dkvolume.Response{Err: err.Error()}
	}

	if err := mountVolume(source, dest, 4); err != nil {
		return dkvolume.Response{Err: err.Error()}
	}
	e.mountm.Add(dest, r.Name)
	return dkvolume.Response{Mountpoint: dest}
}

func (e efsDriver) Unmount(r dkvolume.Request) dkvolume.Response {
	e.m.Lock()
	defer e.m.Unlock()
	dest := mountpoint(e.root, r.Name)
	source := e.fixSource(r.Name)

	if e.mountm.HasMount(dest) {
		if e.mountm.Count(dest) > 1 {
			log.Infof("Skipping unmount for %s - in use by other containers\n", dest)
			e.mountm.Decrement(dest)
			return dkvolume.Response{}
		}
		e.mountm.Decrement(dest)
	}

	log.Infof("Unmounting volume %s from %s\n", source, dest)

	if err := run(fmt.Sprintf("umount %s", dest)); err != nil {
		return dkvolume.Response{Err: err.Error()}
	}

	if err := os.RemoveAll(dest); err != nil {
		return dkvolume.Response{Err: err.Error()}
	}

	return dkvolume.Response{}
}

func (e efsDriver) fixSource(name string) string {
	v := strings.Split(name, "/")
	if e.resolve {
		uri := fmt.Sprintf(EfsTemplateURI, e.availzone, v[0], e.region)
		if i, ok := e.dnscache[uri]; ok {
			return mountSuffix(i)
		}

		log.Debugf("Attempting to resolve: %s", uri)
		if ips, err := net.LookupHost(uri); err == nil {
			log.Debugf("Resolved Addresses: %v", ips)
			e.dnscache[uri] = ips[0]
			return mountSuffix(ips[0])
		} else {
			log.Errorf("Error during resolve: %s", err.Error())
			return mountSuffix(uri)
		}
	}
	v[0] = v[0] + ":"
	return strings.Join(v, "/")
}

func mountSuffix(uri string) string {
	return uri + ":/"
}
