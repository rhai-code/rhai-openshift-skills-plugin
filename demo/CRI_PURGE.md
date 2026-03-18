---
name: cri-purge
description: Explores Disk pressure and high usage of disk space from crio based container images. Based on https://access.redhat.com/solutions/6738851
---

## Check for older container images that can be pruned

Simple check of space available.

Run in dry run mode only - this is quick (a few seconds to run).

The full command is:

```bash
for node in $(oc get node -o name); do
  echo ">> containers storage for $node"
  df -kh /var/lib/containers/

  echo ">> cri-purge for $node"
  oc -n default debug -T $node -- chroot /host bash -s <<EOF
  #!/bin/bash
  cd /tmp
  curl -sLO https://gist.githubusercontent.com/eformat/7d9fb3d2a85fb51ea89ae84e1cefdc58/raw/7cc86ed4e249d92dce63abc26edcfaf4fcd03cb2/cri-purge.sh
  chmod 755 cri-purge.sh
  ./cri-purge.sh -dp
EOF
done
```

If a detailed detailed dry-run is required - include danagling images in the output (takes several minutes to run) replace the cri-purge command line above with:

```bash
  ./cri-purge.sh -dpd
```

In case of disk pressure:

```bash
oc describe node/<node-name> | grep -A 20 "Conditions:"
```

Healthy State: KubeletHasNoDiskPressure = False.

Unhealthy State: KubeletHasNoDiskPressure = True (Nodes may evict pods to free up space).

If the node is Unhealthy - a cri-purge can be run to free space - replace the cri-purge command line above with:

```bash
  ./cri-purge.sh -p
```

## Additional Notes

cri-purge.sh | Version: 0.1.2 | 06/10/2024 | Richard J. Durso

  List and Purge downloaded cached images from containerd.
  -----------------------------------------------------------------------------

  This script requires sudo access to CRICTL (or CRIO-STATUS for OpenShift) to
  obtain a list of cached downloaded images and remove specific older images. 
  It will do best effort to honor semantic versioning always leaving the newest
  version of the downloaded image and only purge previous version(s).

  -h,   --help            : This usage statement.
  -dp,  --dry-run         : Dry run --purge, Do not actually purge any images.
  -dpd, --dry-run-dangling: Same as --dry-run, but include dangling images.
  -l,   --list            : List cached images and purgable versions.
  -p,   --purge           : List images and PURGE/PRUNE older versioned images.
  -pd,  --purge-dangling  : Same as --purge, but include dangling images.
  -s,   --show-dangling   : List dangling images with '<none>' tag.
