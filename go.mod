module github.com/swisstxt/cloudstack-cloud-controller-manager

require (
	github.com/spf13/pflag v1.0.3
	github.com/xanzy/go-cloudstack v2.4.1+incompatible
	gopkg.in/gcfg.v1 v1.2.3
	k8s.io/api v0.0.0-20190805141119-fdd30b57c827
	k8s.io/apimachinery v0.0.0-20190612205821-1799e75a0719
	k8s.io/apiserver v0.0.0-20190805142138-368b2058237c
	k8s.io/cloud-provider v0.0.0-20190805144409-8484242760e7
	k8s.io/component-base v0.0.0-20190805141645-3a5e5ac800ae
	k8s.io/klog v0.3.1
)

replace (
	golang.org/x/sync => golang.org/x/sync v0.0.0-20181108010431-42b317875d0f
	golang.org/x/sys => golang.org/x/sys v0.0.0-20190209173611-3b5209105503
	golang.org/x/tools => golang.org/x/tools v0.0.0-20190313210603-aa82965741a9
	k8s.io/api => k8s.io/api v0.0.0-20190805141119-fdd30b57c827
	k8s.io/apimachinery => k8s.io/apimachinery v0.0.0-20190612205821-1799e75a0719
	k8s.io/apiserver => k8s.io/apiserver v0.0.0-20190805142138-368b2058237c
	k8s.io/client-go => k8s.io/client-go v0.0.0-20190805141520-2fe0317bcee0
	k8s.io/cloud-provider => k8s.io/cloud-provider v0.0.0-20190805144409-8484242760e7
	k8s.io/component-base => k8s.io/component-base v0.0.0-20190805141645-3a5e5ac800ae
)
