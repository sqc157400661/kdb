package v1beta1

type PostgresUserSpec struct {

	// This value goes into the name of a corev1.Secret and a label value, so
	// it must match both IsDNS1123Subdomain and IsValidLabelValue. The pattern
	// below is IsDNS1123Subdomain without any dots, U+002E.

	// The name of this PostgreSQL user. The value may contain only lowercase
	// letters, numbers, and hyphen so that it fits into Kubernetes metadata.
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	// +kubebuilder:validation:Type=string
	Name string `json:"name"`

	// Databases to which this user can connect and create objects. Removing a
	// database from this list does NOT revoke access. This field is ignored for
	// the "postgres" user.
	// +listType=set
	// +optional
	Databases []string `json:"databases,omitempty"`

	// ALTER ROLE options except for PASSWORD. This field is ignored for the
	// "postgres" user.
	// More info: https://www.postgresql.org/docs/current/role-attributes.html
	// +kubebuilder:validation:Pattern=`^[^;]*$`
	// +optional
	Options string `json:"options,omitempty"`

	// Properties of the password generated for this user.
	// +optional
	Password string `json:"password,omitempty"`
}
