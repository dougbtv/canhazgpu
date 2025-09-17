package types

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// CachePlan defines the desired cache state across the cluster
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type CachePlan struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   CachePlanSpec   `json:"spec,omitempty"`
	Status CachePlanStatus `json:"status,omitempty"`
}

type CachePlanSpec struct {
	Items []CacheItem `json:"items,omitempty"`
}

type CacheItem struct {
	Type         CacheItemType `json:"type"`
	Name         string        `json:"name"`
	Scope        string        `json:"scope,omitempty"`        // "allNodes" or "nodeSelector"
	Image        *ImageCache   `json:"image,omitempty"`
	GitRepo      *GitRepoCache `json:"gitRepo,omitempty"`
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`
}

type CacheItemType string

const (
	CacheItemTypeImage       CacheItemType = "image"
	CacheItemTypeGitRepo     CacheItemType = "gitRepo"
	CacheItemTypePythonWheels CacheItemType = "pythonWheels"
	CacheItemTypeHFModel     CacheItemType = "hfModel"
)

type ImageCache struct {
	Ref string `json:"ref"`
}

type GitRepoCache struct {
	URL          string `json:"url"`
	Branch       string `json:"branch,omitempty"`
	Commit       string `json:"commit,omitempty"`
	PathName     string `json:"pathName"`
	SyncStrategy string `json:"syncStrategy,omitempty"` // "hardReset" or "merge"
}

type CachePlanStatus struct {
	Phase      string               `json:"phase,omitempty"` // "Applied", "Degraded", "Progressing"
	Summary    CachePlanSummary     `json:"summary,omitempty"`
	Conditions []metav1.Condition   `json:"conditions,omitempty"`
}

type CachePlanSummary struct {
	TotalItems  int `json:"totalItems"`
	ReadyItems  int `json:"readyItems"`
	FailedItems int `json:"failedItems"`
}

// CachePlanList contains a list of CachePlan
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type CachePlanList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []CachePlan `json:"items"`
}

// NodeCacheStatus reports the actual cache state on a specific node
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type NodeCacheStatus struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   NodeCacheSpec   `json:"spec,omitempty"`
	Status NodeCacheStatusData `json:"status,omitempty"`
}

type NodeCacheSpec struct {
	// Empty - status only
}

type NodeCacheStatusData struct {
	NodeName   string           `json:"nodeName,omitempty"`
	Images     []ImageStatus    `json:"images,omitempty"`
	GitRepos   []GitRepoStatus  `json:"gitRepos,omitempty"`
	Errors     []string         `json:"errors,omitempty"`
	LastUpdate *metav1.Time     `json:"lastUpdate,omitempty"`
}

type ImageStatus struct {
	Ref         string       `json:"ref"`
	Present     bool         `json:"present"`
	Digest      string       `json:"digest,omitempty"`
	LastChecked *metav1.Time `json:"lastChecked,omitempty"`
	Message     string       `json:"message,omitempty"`
}

type GitRepoStatus struct {
	Name     string       `json:"name"`
	Path     string       `json:"path"`
	URL      string       `json:"url"`
	Branch   string       `json:"branch,omitempty"`
	Commit   string       `json:"commit,omitempty"`
	Synced   bool         `json:"synced"`
	LastSync *metav1.Time `json:"lastSync,omitempty"`
	Message  string       `json:"message,omitempty"`
}

// NodeCacheStatusList contains a list of NodeCacheStatus
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type NodeCacheStatusList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NodeCacheStatus `json:"items"`
}