package controller

// These compatibility markers preserve permissions historically shipped in
// config/rbac/role.yaml while controller-gen owns the generated role. They are
// intentionally kept separate from PostgreSQL's additive permissions so
// regenerating manifests cannot break existing platform controllers.

// +kubebuilder:rbac:groups="",resources=namespaces,verbs=create;get;list;patch;update;watch
// +kubebuilder:rbac:groups=apps,resources=daemonsets,verbs=create;delete;get;list;patch;update;watch
// +kubebuilder:rbac:groups=kdb.com,resources=kdbclusters,verbs=get;list;patch;watch
// +kubebuilder:rbac:groups=kdb.com,resources=kdbclusters/status,verbs=patch
// +kubebuilder:rbac:groups=kdb.com,resources=kdbmonitoringstacks,verbs=get;list;watch
// +kubebuilder:rbac:groups=kdb.com,resources=kdbmonitoringstacks/status,verbs=patch
// +kubebuilder:rbac:groups=kdb.com,resources=kdblogsystems,verbs=get;list;watch
// +kubebuilder:rbac:groups=kdb.com,resources=kdblogsystems/status,verbs=patch
// +kubebuilder:rbac:groups=monitoring.coreos.com,resources=alertmanagers;alertmanagerconfigs;podmonitors;probes;prometheuses;prometheusagents;prometheusrules;scrapeconfigs;servicemonitors;thanosrulers,verbs=create;delete;get;list;patch;update;watch
// +kubebuilder:rbac:groups=apiextensions.k8s.io,resources=customresourcedefinitions,verbs=create;get;list;patch;update;watch
// +kubebuilder:rbac:groups=dbpaas.com,resources=postgresclusters,verbs=get;list;watch
// +kubebuilder:rbac:groups=dbpaas.com,resources=postgresclusters/status,verbs=patch
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=clusterrolebindings,verbs=create;get;list;patch;update;watch
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=clusterroles,verbs=bind;create;escalate;get;list;patch;update;watch
