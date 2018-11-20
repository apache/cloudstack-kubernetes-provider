# Cloudstack Cloud Controller Manager for Kubernetes

© 2018 SWISS TXT AG
All rights reserved.

Based on the old Cloudstack provider in Kubernetes.

## Build

All dependencies are vendored.
You need GNU make, git and Go 1.10 to build cloudstack-ccm:

```bash
make
```

## Use

Make sure your apiserver is running locally and keep your cloudstack config ready:

```bash
./cloudstack-ccm --cloud-provider external-cloudstack --cloud-config cloud.config --master localhost
```
