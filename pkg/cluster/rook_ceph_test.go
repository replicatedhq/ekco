package cluster

import (
	"context"
	_ "embed"
	"fmt"
	"reflect"
	"testing"

	"github.com/blang/semver"
	"github.com/golang/mock/gomock"
	mock_k8s "github.com/replicatedhq/ekco/pkg/k8s/mock"
	"github.com/replicatedhq/ekco/pkg/logger"
	"github.com/replicatedhq/ekco/pkg/util"
	cephv1 "github.com/rook/rook/pkg/apis/ceph.rook.io/v1"
	rookfake "github.com/rook/rook/pkg/client/clientset/versioned/fake"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
)

func TestParseCephOSDStatusHosts(t *testing.T) {
	tests := []struct {
		name  string
		s     string
		count int
	}{
		{
			name: "2 unique",
			s: `
ID  HOST             USED  AVAIL  WR OPS  WR DATA  RD OPS  RD DATA  STATE
 0  areed-aka-36xk  9.77G   190G      0      123k      6      304   exists,up
 1  areed-aka-cpnv  9.77G   190G      0     2457       2      113   exists,up`,
			count: 2,
		},
		{
			name:  "0",
			s:     `ID  HOST             USED  AVAIL  WR OPS  WR DATA  RD OPS  RD DATA  STATE`,
			count: 0,
		},
		{
			name: "5 unique of 11",
			s: `
ID  HOST             USED  AVAIL  WR OPS  WR DATA  RD OPS  RD DATA  STATE
 0  areed-aka-36xk  9.77G   190G      0      123k      6      304   exists,up
 1  areed-aka-cpnv  9.77G   190G      0     2457       2      113   exists,up
 2  areed-aka-abcd  9.77G   190G      0      123k      6      304   exists,up
 3  areed-aka-abce  9.77G   190G      0      123k      6      304   exists,up
 4  areed-aka-abdf  9.77G   190G      0      123k      6      304   exists,up
 5  areed-aka-36xk  9.77G   190G      0      123k      6      304   exists,up
 6  areed-aka-cpnv  9.77G   190G      0      123k      6      304   exists,up
 7  areed-aka-abcd  9.77G   190G      0      123k      6      304   exists,up
 8  areed-aka-abce  9.77G   190G      0      123k      6      304   exists,up
 9  areed-aka-abdf  9.77G   190G      0      123k      6      304   exists,up
10  areed-aka-36xk  9.77G   190G      0      123k      6      304   exists,up`,
			count: 5,
		},
		{
			name: "Rook 1.0",
			s: `
+----+----------------+-------+-------+--------+---------+--------+---------+-----------+
| id |      host      |  used | avail | wr ops | wr data | rd ops | rd data |   state   |
+----+----------------+-------+-------+--------+---------+--------+---------+-----------+
| 0  | areed-aka-81k8 | 14.6G |  179G |    0   |     0   |    1   |     0   | exists,up |
| 1  | areed-aka-942c | 14.3G |  179G |    1   |  7372   |    1   |     0   | exists,up |
+----+----------------+-------+-------+--------+---------+--------+---------+-----------+`,
			count: 2,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actual, err := parseCephOSDStatusHosts(test.s)
			if err != nil {
				t.Fatal(err)
			}
			count := len(actual)
			if test.count != count {
				t.Errorf("got %d, want %d", count, test.count)
			}
		})
	}
}

func TestController_SetCephCSIResources(t *testing.T) {
	configMap := &corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "ConfigMap",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "rook-ceph-operator-config",
			Namespace: "rook-ceph",
		},
		Data: map[string]string{},
	}

	type args struct {
		nodeCount int
	}
	tests := []struct {
		name        string
		resources   []runtime.Object
		rookVersion semver.Version
		args        args
		want        bool
		wantErr     bool
	}{
		{
			name:        "1 node",
			resources:   []runtime.Object{configMap},
			rookVersion: semver.MustParse("1.9.12"),
			args: args{
				nodeCount: 1,
			},
			want: false,
		},
		{
			name:        "3 nodes",
			resources:   []runtime.Object{configMap},
			rookVersion: semver.MustParse("1.9.12"),
			args: args{
				nodeCount: 3,
			},
			want: true,
		},
		{
			name:        "rook 1.8",
			resources:   []runtime.Object{configMap},
			rookVersion: semver.MustParse("1.8.10"),
			args: args{
				nodeCount: 3,
			},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clientset := fake.NewSimpleClientset(tt.resources...)
			c := &Controller{
				Config: ControllerConfig{
					Client: clientset,
				},
				Log: logger.NewDiscardLogger(),
			}
			got, err := c.SetCephCSIResources(context.Background(), tt.rookVersion, tt.args.nodeCount)
			if (err != nil) != tt.wantErr {
				t.Errorf("Controller.SetCephCSIResources() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("Controller.SetCephCSIResources() = %v, want %v", got, tt.want)
			}

			got, err = c.SetCephCSIResources(context.Background(), tt.rookVersion, tt.args.nodeCount)
			if (err != nil) != tt.wantErr {
				t.Errorf("Controller.SetCephCSIResources() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != false {
				t.Errorf("Controller.SetCephCSIResources() = %v, want %v", got, false)
			}
		})
	}
}

func TestController_GetRookVersion(t *testing.T) {
	rookCephOperatorDeployment := func(image string) *appsv1.Deployment {
		return &appsv1.Deployment{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "apps/v1",
				Kind:       "Deployment",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "rook-ceph-operator",
				Namespace: "rook-ceph",
			},
			Spec: appsv1.DeploymentSpec{
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Name:  "rook-ceph-operator",
								Image: image,
							},
						},
					},
				},
			},
		}
	}

	newSemver := func(str string) *semver.Version {
		version := semver.MustParse(str)
		return &version
	}

	tests := []struct {
		name              string
		resources         []runtime.Object
		want              *semver.Version
		wantErr           bool
		wantIsNotFoundErr bool
	}{
		{
			name: "1.9.12",
			resources: []runtime.Object{
				rookCephOperatorDeployment("rook/rook-ceph:v1.9.12"),
				&corev1.Namespace{TypeMeta: metav1.TypeMeta{Kind: "Namespace", APIVersion: "v1"}, ObjectMeta: metav1.ObjectMeta{Name: "rook-ceph"}},
			},
			want: newSemver("1.9.12"),
		},
		{
			name: "1.0.4-9065b09-20210625",
			resources: []runtime.Object{
				rookCephOperatorDeployment("kurlsh/rook-ceph:v1.0.4-9065b09-20210625"),
				&corev1.Namespace{TypeMeta: metav1.TypeMeta{Kind: "Namespace", APIVersion: "v1"}, ObjectMeta: metav1.ObjectMeta{Name: "rook-ceph"}},
			},
			want: newSemver("1.0.4-9065b09-20210625"),
		},
		{
			name: "invalid semver",
			resources: []runtime.Object{
				rookCephOperatorDeployment("kurlsh/rook-ceph:not-semver"),
				&corev1.Namespace{TypeMeta: metav1.TypeMeta{Kind: "Namespace", APIVersion: "v1"}, ObjectMeta: metav1.ObjectMeta{Name: "rook-ceph"}},
			},
			wantErr: true,
		},
		{
			name: "not found",
			resources: []runtime.Object{
				&corev1.Namespace{TypeMeta: metav1.TypeMeta{Kind: "Namespace", APIVersion: "v1"}, ObjectMeta: metav1.ObjectMeta{Name: "rook-ceph"}},
			},
			wantErr:           true,
			wantIsNotFoundErr: true,
		},
		{
			name:              "no namespace",
			resources:         []runtime.Object{},
			wantErr:           true,
			wantIsNotFoundErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clientset := fake.NewSimpleClientset(tt.resources...)
			c := &Controller{
				Config: ControllerConfig{
					Client: clientset,
				},
				Log: logger.NewDiscardLogger(),
			}
			got, err := c.GetRookVersion(context.Background())
			if (err != nil) != tt.wantErr {
				t.Errorf("Controller.GetRookVersion() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantIsNotFoundErr {
				if !util.IsNotFoundErr(err) {
					t.Errorf("Controller.GetRookVersion() error = %T, want k8serrors.IsNotFound", err)
				}
			}
			if !tt.wantErr {
				if !(*tt.want).Equals(*got) {
					t.Errorf("Controller.GetRookVersion() = %s, want %s", *got, *tt.want)
				}
			}
		})
	}
}

func TestController_UseNodesForStorage(t *testing.T) {
	type args struct {
		rookVersion semver.Version
		names       []string
	}
	tests := []struct {
		name                 string
		nodes                []string
		rookResources        []runtime.Object
		args                 args
		want                 int
		wantStorageScopeSpec cephv1.StorageScopeSpec
		wantErr              bool
	}{
		{
			name:  "storage nodes increase from useAllNodes to 1, rook version 1.9.12",
			nodes: []string{"node1"},
			rookResources: []runtime.Object{
				&cephv1.CephCluster{
					TypeMeta: metav1.TypeMeta{Kind: "CephCluster", APIVersion: "ceph.rook.io/v1"},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "rook-ceph",
						Namespace: "rook-ceph",
					},
					Spec: cephv1.ClusterSpec{
						Storage: cephv1.StorageScopeSpec{
							UseAllNodes: true,
						},
					},
				},
			},
			args: args{
				rookVersion: semver.MustParse("1.9.12"),
				names:       []string{"node1"},
			},
			want: 1,
			wantStorageScopeSpec: cephv1.StorageScopeSpec{
				UseAllNodes: false,
				Nodes: []cephv1.Node{
					{
						Name: "node1",
					},
				},
			},
			wantErr: false,
		},
		{
			name:  "storage nodes stays at 1, rook version 1.9.12",
			nodes: []string{"node1"},
			rookResources: []runtime.Object{
				&cephv1.CephCluster{
					TypeMeta: metav1.TypeMeta{Kind: "CephCluster", APIVersion: "ceph.rook.io/v1"},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "rook-ceph",
						Namespace: "rook-ceph",
					},
					Spec: cephv1.ClusterSpec{
						Storage: cephv1.StorageScopeSpec{
							UseAllNodes: false,
							Nodes: []cephv1.Node{
								{
									Name: "node1",
								},
							},
						},
					},
				},
			},
			args: args{
				rookVersion: semver.MustParse("1.9.12"),
				names:       []string{"node1"},
			},
			want: 1,
			wantStorageScopeSpec: cephv1.StorageScopeSpec{
				UseAllNodes: false,
				Nodes: []cephv1.Node{
					{
						Name: "node1",
					},
				},
			},
			wantErr: false,
		},
		{
			name:  "storage nodes increase from 1 to 3, rook version 1.9.12",
			nodes: []string{"node1", "node2", "node3"},
			rookResources: []runtime.Object{
				&cephv1.CephCluster{
					TypeMeta: metav1.TypeMeta{Kind: "CephCluster", APIVersion: "ceph.rook.io/v1"},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "rook-ceph",
						Namespace: "rook-ceph",
					},
					Spec: cephv1.ClusterSpec{
						Storage: cephv1.StorageScopeSpec{
							UseAllNodes: false,
							Nodes: []cephv1.Node{
								{
									Name: "node1",
								},
							},
						},
					},
				},
			},
			args: args{
				rookVersion: semver.MustParse("1.9.12"),
				names:       []string{"node1", "node2", "node3"},
			},
			want: 3,
			wantStorageScopeSpec: cephv1.StorageScopeSpec{
				Nodes: []cephv1.Node{
					{
						Name: "node1",
					},
					{
						Name: "node2",
					},
					{
						Name: "node3",
					},
				},
			},
			wantErr: false,
		},
		// TODO: rookVersion 1.0.4
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			// TODO: deploy/rook-ceph-operator for rookVersion 1.0.4
			resources := []runtime.Object{
				&corev1.Pod{
					TypeMeta: metav1.TypeMeta{Kind: "Pod", APIVersion: "v1"},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "rook-ceph-tools-5b8b8b8b8b-5b8b8",
						Namespace: "rook-ceph",
						Labels: map[string]string{
							"app": "rook-ceph-tools",
						},
					},
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Name: "rook-ceph-tools",
							},
						},
					},
				},
			}
			cephOSDStatusOut := "ID  HOST   USED  AVAIL  WR OPS  WR DATA  RD OPS  RD DATA  STATE"
			for _, node := range tt.nodes {
				resources = append(resources, &corev1.Node{
					TypeMeta: metav1.TypeMeta{Kind: "Node", APIVersion: "v1"},
					ObjectMeta: metav1.ObjectMeta{
						Name: node,
					},
				})
				cephOSDStatusOut += fmt.Sprintf("\n0  %s   128M  99.8G      0        0       2       84   exists,up", node)
			}

			m := mock_k8s.NewMockSyncExecutorInterface(ctrl)
			m.EXPECT().ExecContainer(gomock.Any(), "rook-ceph", "rook-ceph-tools-5b8b8b8b8b-5b8b8", "rook-ceph-tools", "ceph", "osd", "status").
				Return(0, cephOSDStatusOut, "", nil)

			clientset := fake.NewSimpleClientset(resources...)
			rookClientset := rookfake.NewSimpleClientset(tt.rookResources...)

			c := &Controller{
				Config: ControllerConfig{
					Client: clientset,
					CephV1: rookClientset.CephV1(),
				},
				SyncExecutor: m,
				Log:          logger.NewDiscardLogger(),
			}
			got, err := c.UseNodesForStorage(tt.args.rookVersion, tt.args.names)
			if (err != nil) != tt.wantErr {
				t.Errorf("Controller.UseNodesForStorage() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("Controller.UseNodesForStorage() = %v, want %v", got, tt.want)
			}
			cephCluster, err := rookClientset.CephV1().CephClusters("rook-ceph").Get(context.Background(), "rook-ceph", metav1.GetOptions{})
			if err != nil {
				t.Errorf("CephClusters.Get(\"rook-ceph\") error = %v", err)
				return
			}
			if !reflect.DeepEqual(cephCluster.Spec.Storage, tt.wantStorageScopeSpec) {
				t.Errorf("Controller.UseNodesForStorage() = %v, want %v", cephCluster.Spec.Storage, tt.wantStorageScopeSpec)
			}
		})
	}
}

func TestController_removeCephClusterStorageNode(t *testing.T) {
	type args struct {
		name string
	}
	tests := []struct {
		name                 string
		nodes                []string
		rookResources        []runtime.Object
		args                 args
		wantStorageScopeSpec cephv1.StorageScopeSpec
		wantErr              bool
	}{
		{
			name:  "storage nodes decrease from 4 to 3, rook version 1.9.12",
			nodes: []string{"node1", "node2", "node3", "node4"},
			rookResources: []runtime.Object{
				&cephv1.CephCluster{
					TypeMeta: metav1.TypeMeta{Kind: "CephCluster", APIVersion: "ceph.rook.io/v1"},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "rook-ceph",
						Namespace: "rook-ceph",
					},
					Spec: cephv1.ClusterSpec{
						Storage: cephv1.StorageScopeSpec{
							UseAllNodes: false,
							Nodes: []cephv1.Node{
								{
									Name: "node1",
								},
								{
									Name: "node2",
								},
								{
									Name: "node3",
								},
								{
									Name: "node4",
								},
							},
						},
					},
				},
			},
			args: args{
				name: "node2",
			},
			wantStorageScopeSpec: cephv1.StorageScopeSpec{
				UseAllNodes: false,
				Nodes: []cephv1.Node{
					{
						Name: "node1",
					},
					{
						Name: "node3",
					},
					{
						Name: "node4",
					},
				},
			},
			wantErr: false,
		},
		{
			name:  "storage node not found, rook version 1.9.12",
			nodes: []string{"node1", "node2", "node3"},
			rookResources: []runtime.Object{
				&cephv1.CephCluster{
					TypeMeta: metav1.TypeMeta{Kind: "CephCluster", APIVersion: "ceph.rook.io/v1"},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "rook-ceph",
						Namespace: "rook-ceph",
					},
					Spec: cephv1.ClusterSpec{
						Storage: cephv1.StorageScopeSpec{
							UseAllNodes: false,
							Nodes: []cephv1.Node{
								{
									Name: "node1",
								},
								{
									Name: "node2",
								},
								{
									Name: "node3",
								},
							},
						},
					},
				},
			},
			args: args{
				name: "node4",
			},
			wantStorageScopeSpec: cephv1.StorageScopeSpec{
				UseAllNodes: false,
				Nodes: []cephv1.Node{
					{
						Name: "node1",
					},
					{
						Name: "node2",
					},
					{
						Name: "node3",
					},
				},
			},
			wantErr: false,
		},
		{
			name:  "useAllNodes true, rook version 1.9.12",
			nodes: []string{"node1", "node2", "node3"},
			rookResources: []runtime.Object{
				&cephv1.CephCluster{
					TypeMeta: metav1.TypeMeta{Kind: "CephCluster", APIVersion: "ceph.rook.io/v1"},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "rook-ceph",
						Namespace: "rook-ceph",
					},
					Spec: cephv1.ClusterSpec{
						Storage: cephv1.StorageScopeSpec{
							UseAllNodes: true,
						},
					},
				},
			},
			args: args{
				name: "node4",
			},
			wantStorageScopeSpec: cephv1.StorageScopeSpec{
				UseAllNodes: true,
			},
			wantErr: false,
		},
		// TODO: rookVersion 1.0.4
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resources := []runtime.Object{}
			for _, node := range tt.nodes {
				resources = append(resources, &corev1.Node{
					TypeMeta: metav1.TypeMeta{Kind: "Node", APIVersion: "v1"},
					ObjectMeta: metav1.ObjectMeta{
						Name: node,
					},
				})
			}

			clientset := fake.NewSimpleClientset(resources...)
			rookClientset := rookfake.NewSimpleClientset(tt.rookResources...)

			c := &Controller{
				Config: ControllerConfig{
					Client: clientset,
					CephV1: rookClientset.CephV1(),
				},
				Log: logger.NewDiscardLogger(),
			}
			err := c.removeCephClusterStorageNode(tt.args.name)
			if (err != nil) != tt.wantErr {
				t.Errorf("Controller.removeCephClusterStorageNode() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			cephCluster, err := rookClientset.CephV1().CephClusters("rook-ceph").Get(context.Background(), "rook-ceph", metav1.GetOptions{})
			if err != nil {
				t.Errorf("CephClusters.Get(\"rook-ceph\") error = %v", err)
				return
			}
			if !reflect.DeepEqual(cephCluster.Spec.Storage, tt.wantStorageScopeSpec) {
				t.Errorf("Controller.UseNodesForStorage() = %v, want %v", cephCluster.Spec.Storage, tt.wantStorageScopeSpec)
			}
		})
	}
}

func TestController_SetBlockPoolReplication(t *testing.T) {
	type args struct {
		rookVersion     semver.Version
		name            string
		level           int
		cephcluster     *cephv1.CephCluster
		doFullReconcile bool
	}
	tests := []struct {
		name          string
		rookResources []runtime.Object
		args          args
		want          bool
		wantLevel     uint
		wantErr       bool
	}{
		{
			name: "blockpool replication stays at 1, rook version 1.9.12",
			rookResources: []runtime.Object{
				&cephv1.CephBlockPool{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "ceph.rook.io/v1",
						Kind:       "CephBlockPool",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "replicapool",
						Namespace: "rook-ceph",
					},
					Spec: cephv1.NamedBlockPoolSpec{
						Name: "replicapool",
						PoolSpec: cephv1.PoolSpec{
							Replicated: cephv1.ReplicatedSpec{
								Size: 1,
							},
						},
					},
				},
			},
			args: args{
				rookVersion:     semver.MustParse("1.9.12"),
				name:            "replicapool",
				level:           1,
				cephcluster:     nil,
				doFullReconcile: false,
			},
			want:      false,
			wantLevel: 1,
			wantErr:   false,
		},
		{
			name: "blockpool replication stays at 1, do full reconcile, rook version 1.9.12",
			rookResources: []runtime.Object{
				&cephv1.CephBlockPool{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "ceph.rook.io/v1",
						Kind:       "CephBlockPool",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "replicapool",
						Namespace: "rook-ceph",
					},
					Spec: cephv1.NamedBlockPoolSpec{
						Name: "replicapool",
						PoolSpec: cephv1.PoolSpec{
							Replicated: cephv1.ReplicatedSpec{
								Size: 1,
							},
						},
					},
				},
			},
			args: args{
				rookVersion:     semver.MustParse("1.9.12"),
				name:            "replicapool",
				level:           1,
				cephcluster:     nil,
				doFullReconcile: true,
			},
			want:      true,
			wantLevel: 1,
			wantErr:   false,
		},
		{
			name: "blockpool replication increase from 1 to 3, rook version 1.9.12",
			rookResources: []runtime.Object{
				&cephv1.CephBlockPool{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "ceph.rook.io/v1",
						Kind:       "CephBlockPool",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "replicapool",
						Namespace: "rook-ceph",
					},
					Spec: cephv1.NamedBlockPoolSpec{
						Name: "replicapool",
						PoolSpec: cephv1.PoolSpec{
							Replicated: cephv1.ReplicatedSpec{
								Size: 1,
							},
						},
					},
				},
			},
			args: args{
				rookVersion:     semver.MustParse("1.9.12"),
				name:            "replicapool",
				level:           3,
				cephcluster:     nil,
				doFullReconcile: false,
			},
			want:      true,
			wantLevel: 3,
			wantErr:   false,
		},
		// TODO: rookVersion 1.0.4
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rookClientset := rookfake.NewSimpleClientset(tt.rookResources...)
			c := &Controller{
				Config: ControllerConfig{
					CephV1: rookClientset.CephV1(),
				},
				Log: logger.NewDiscardLogger(),
			}
			got, err := c.SetBlockPoolReplication(tt.args.rookVersion, tt.args.name, tt.args.level, tt.args.cephcluster, tt.args.doFullReconcile)
			if (err != nil) != tt.wantErr {
				t.Errorf("Controller.SetBlockPoolReplication() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("Controller.SetBlockPoolReplication() = %v, want %v", got, tt.want)
			}
			cephBP, err := rookClientset.CephV1().CephBlockPools("rook-ceph").Get(context.Background(), "replicapool", metav1.GetOptions{})
			if err != nil {
				t.Errorf("CephBlockPools.Get(\"replicapool\") error = %v", err)
				return
			}
			if cephBP.Spec.Replicated.Size != tt.wantLevel {
				t.Errorf("CephBlockPool.Spec.Replicated.Size = %d, want %d", cephBP.Spec.Replicated.Size, tt.wantLevel)
			}
		})
	}
}

func TestController_ReconcileMonCount(t *testing.T) {
	type args struct {
		nodeCount int
	}
	tests := []struct {
		name          string
		rookResources []runtime.Object
		args          args
		wantMonCount  int
		wantErr       bool
	}{
		{
			name: "mon count stays at 1",
			rookResources: []runtime.Object{
				&cephv1.CephCluster{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "ceph.rook.io/v1",
						Kind:       "CephCluster",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "rook-ceph",
						Namespace: "rook-ceph",
					},
					Spec: cephv1.ClusterSpec{
						Mon: cephv1.MonSpec{
							Count: 1,
						},
					},
				},
			},
			args: args{
				nodeCount: 1,
			},
			wantMonCount: 1,
		},
		{
			name: "mon count increase from 1 to 6",
			rookResources: []runtime.Object{
				&cephv1.CephCluster{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "ceph.rook.io/v1",
						Kind:       "CephCluster",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "rook-ceph",
						Namespace: "rook-ceph",
					},
					Spec: cephv1.ClusterSpec{
						Mon: cephv1.MonSpec{
							Count: 1,
						},
					},
				},
			},
			args: args{
				nodeCount: 6,
			},
			wantMonCount: 3,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rookClientset := rookfake.NewSimpleClientset(tt.rookResources...)
			c := &Controller{
				Config: ControllerConfig{
					CephV1: rookClientset.CephV1(),
				},
				Log: logger.NewDiscardLogger(),
			}
			if err := c.ReconcileMonCount(context.Background(), tt.args.nodeCount); (err != nil) != tt.wantErr {
				t.Errorf("Controller.ReconcileMonCount() error = %v, wantErr %v", err, tt.wantErr)
			}
			cephCluster, err := rookClientset.CephV1().CephClusters("rook-ceph").Get(context.Background(), "rook-ceph", metav1.GetOptions{})
			if err != nil {
				t.Errorf("CephClusters.Get(\"rook-ceph\") error = %v", err)
				return
			}
			if cephCluster.Spec.Mon.Count != tt.wantMonCount {
				t.Errorf("CephCluster.Spec.Mon.Count = %d, want %d", cephCluster.Spec.Mon.Count, tt.wantMonCount)
			}
		})
	}
}

func TestController_ReconcileMgrCount(t *testing.T) {
	type args struct {
		rookVersion semver.Version
		nodeCount   int
	}
	tests := []struct {
		name          string
		rookResources []runtime.Object
		args          args
		wantMgrCount  int
		wantErr       bool
	}{
		{
			name: "mgr count stays at 1, rook version 1.9.12",
			rookResources: []runtime.Object{
				&cephv1.CephCluster{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "ceph.rook.io/v1",
						Kind:       "CephCluster",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "rook-ceph",
						Namespace: "rook-ceph",
					},
					Spec: cephv1.ClusterSpec{
						Mgr: cephv1.MgrSpec{
							Count: 1,
						},
					},
				},
			},
			args: args{
				rookVersion: semver.MustParse("1.9.12"),
				nodeCount:   1,
			},
			wantMgrCount: 1,
		},
		{
			name: "mgr count increase from 1 to 3, rook version 1.9.12",
			rookResources: []runtime.Object{
				&cephv1.CephCluster{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "ceph.rook.io/v1",
						Kind:       "CephCluster",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "rook-ceph",
						Namespace: "rook-ceph",
					},
					Spec: cephv1.ClusterSpec{
						Mgr: cephv1.MgrSpec{
							Count: 1,
						},
					},
				},
			},
			args: args{
				rookVersion: semver.MustParse("1.9.12"),
				nodeCount:   3,
			},
			wantMgrCount: 2,
		},
		{
			name: "mgr count increase from 1 to 3, rook version 1.8.10",
			rookResources: []runtime.Object{
				&cephv1.CephCluster{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "ceph.rook.io/v1",
						Kind:       "CephCluster",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "rook-ceph",
						Namespace: "rook-ceph",
					},
					Spec: cephv1.ClusterSpec{
						Mgr: cephv1.MgrSpec{
							Count: 1,
						},
					},
				},
			},
			args: args{
				rookVersion: semver.MustParse("1.8.10"),
				nodeCount:   3,
			},
			wantMgrCount: 1,
		},
		// TODO: rookVersion 1.0.4
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rookClientset := rookfake.NewSimpleClientset(tt.rookResources...)
			c := &Controller{
				Config: ControllerConfig{
					CephV1: rookClientset.CephV1(),
				},
				Log: logger.NewDiscardLogger(),
			}
			if err := c.ReconcileMgrCount(context.Background(), tt.args.rookVersion, tt.args.nodeCount); (err != nil) != tt.wantErr {
				t.Errorf("Controller.ReconcileMgrCount() error = %v, wantErr %v", err, tt.wantErr)
			}
			cephCluster, err := rookClientset.CephV1().CephClusters("rook-ceph").Get(context.Background(), "rook-ceph", metav1.GetOptions{})
			if err != nil {
				t.Errorf("CephClusters.Get(\"rook-ceph\") error = %v", err)
				return
			}
			if cephCluster.Spec.Mgr.Count != tt.wantMgrCount {
				t.Errorf("CephCluster.Spec.Mgr.Count = %d, want %d", cephCluster.Spec.Mgr.Count, tt.wantMgrCount)
			}
		})
	}
}

func TestController_SetFilesystemReplication(t *testing.T) {
	type args struct {
		rookVersion     semver.Version
		name            string
		level           int
		cephcluster     *cephv1.CephCluster
		doFullReconcile bool
	}
	tests := []struct {
		name          string
		rookResources []runtime.Object
		args          args
		want          bool
		wantLevel     uint
		wantErr       bool
	}{
		{
			name: "filesystem replication stays at 1, rook version 1.9.12",
			rookResources: []runtime.Object{
				&cephv1.CephFilesystem{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "ceph.rook.io/v1",
						Kind:       "CephFilesystem",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "myfs",
						Namespace: "rook-ceph",
					},
					Spec: cephv1.FilesystemSpec{
						MetadataPool: cephv1.PoolSpec{
							Replicated: cephv1.ReplicatedSpec{
								Size: 1,
							},
						},
						DataPools: []cephv1.NamedPoolSpec{
							{
								Name: "myfs-data0",
								PoolSpec: cephv1.PoolSpec{
									Replicated: cephv1.ReplicatedSpec{
										Size: 1,
									},
								},
							},
						},
					},
				},
			},
			args: args{
				rookVersion:     semver.MustParse("1.9.12"),
				name:            "myfs",
				level:           1,
				cephcluster:     nil,
				doFullReconcile: false,
			},
			want:      false,
			wantLevel: 1,
			wantErr:   false,
		},
		{
			name: "filesystem replication stays at 1, do full reconcile, rook version 1.9.12",
			rookResources: []runtime.Object{
				&cephv1.CephFilesystem{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "ceph.rook.io/v1",
						Kind:       "CephFilesystem",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "myfs",
						Namespace: "rook-ceph",
					},
					Spec: cephv1.FilesystemSpec{
						MetadataPool: cephv1.PoolSpec{
							Replicated: cephv1.ReplicatedSpec{
								Size: 1,
							},
						},
						DataPools: []cephv1.NamedPoolSpec{
							{
								Name: "myfs-data0",
								PoolSpec: cephv1.PoolSpec{
									Replicated: cephv1.ReplicatedSpec{
										Size: 1,
									},
								},
							},
						},
					},
				},
			},
			args: args{
				rookVersion:     semver.MustParse("1.9.12"),
				name:            "myfs",
				level:           1,
				cephcluster:     nil,
				doFullReconcile: true,
			},
			want:      true,
			wantLevel: 1,
			wantErr:   false,
		},
		{
			name: "filesystem replication increase from 1 to 3, rook version 1.9.12",
			rookResources: []runtime.Object{
				&cephv1.CephFilesystem{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "ceph.rook.io/v1",
						Kind:       "CephFilesystem",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "myfs",
						Namespace: "rook-ceph",
					},
					Spec: cephv1.FilesystemSpec{
						MetadataPool: cephv1.PoolSpec{
							Replicated: cephv1.ReplicatedSpec{
								Size: 1,
							},
						},
						DataPools: []cephv1.NamedPoolSpec{
							{
								Name: "myfs-data0",
								PoolSpec: cephv1.PoolSpec{
									Replicated: cephv1.ReplicatedSpec{
										Size: 1,
									},
								},
							},
						},
					},
				},
			},
			args: args{
				rookVersion:     semver.MustParse("1.9.12"),
				name:            "myfs",
				level:           3,
				cephcluster:     nil,
				doFullReconcile: false,
			},
			want:      true,
			wantLevel: 3,
			wantErr:   false,
		},
		// TODO: rookVersion 1.0.4
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rookClientset := rookfake.NewSimpleClientset(tt.rookResources...)
			c := &Controller{
				Config: ControllerConfig{
					CephV1: rookClientset.CephV1(),
				},
				Log: logger.NewDiscardLogger(),
			}
			got, err := c.SetFilesystemReplication(tt.args.rookVersion, tt.args.name, tt.args.level, tt.args.cephcluster, tt.args.doFullReconcile)
			if (err != nil) != tt.wantErr {
				t.Errorf("Controller.SetFilesystemReplication() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("Controller.SetFilesystemReplication() = %v, want %v", got, tt.want)
			}
			cephFs, err := rookClientset.CephV1().CephFilesystems("rook-ceph").Get(context.Background(), "myfs", metav1.GetOptions{})
			if err != nil {
				t.Errorf("CephFilesystems.Get(\"myfs\") error = %v", err)
				return
			}
			if cephFs.Spec.MetadataPool.Replicated.Size != tt.wantLevel {
				t.Errorf("CephFilesystem.Spec.MetadataPool.Replicated.Size = %d, want %d", cephFs.Spec.MetadataPool.Replicated.Size, tt.wantLevel)
			}
			if cephFs.Spec.DataPools[0].Replicated.Size != tt.wantLevel {
				t.Errorf("CephFilesystem.Spec.DataPools[0].Replicated.Size = %d, want %d", cephFs.Spec.DataPools[0].Replicated.Size, tt.wantLevel)
			}
		})
	}
}

func TestController_SetObjectStoreReplication(t *testing.T) {
	type args struct {
		rookVersion     semver.Version
		name            string
		level           int
		cephcluster     *cephv1.CephCluster
		doFullReconcile bool
	}
	tests := []struct {
		name          string
		rookResources []runtime.Object
		args          args
		want          bool
		wantLevel     uint
		wantErr       bool
	}{
		{
			name: "objectstore replication stays at 1, rook version 1.9.12",
			rookResources: []runtime.Object{
				&cephv1.CephObjectStore{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "ceph.rook.io/v1",
						Kind:       "CephObjectStore",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "my-store",
						Namespace: "rook-ceph",
					},
					Spec: cephv1.ObjectStoreSpec{
						MetadataPool: cephv1.PoolSpec{
							Replicated: cephv1.ReplicatedSpec{
								Size: 1,
							},
						},
						DataPool: cephv1.PoolSpec{
							Replicated: cephv1.ReplicatedSpec{
								Size: 1,
							},
						},
					},
				},
			},
			args: args{
				rookVersion:     semver.MustParse("1.9.12"),
				name:            "my-store",
				level:           1,
				cephcluster:     nil,
				doFullReconcile: false,
			},
			want:      false,
			wantLevel: 1,
			wantErr:   false,
		},
		{
			name: "objectstore replication stays at 1, do full reconcile, rook version 1.9.12",
			rookResources: []runtime.Object{
				&cephv1.CephObjectStore{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "ceph.rook.io/v1",
						Kind:       "CephObjectStore",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "my-store",
						Namespace: "rook-ceph",
					},
					Spec: cephv1.ObjectStoreSpec{
						MetadataPool: cephv1.PoolSpec{
							Replicated: cephv1.ReplicatedSpec{
								Size: 1,
							},
						},
						DataPool: cephv1.PoolSpec{
							Replicated: cephv1.ReplicatedSpec{
								Size: 1,
							},
						},
					},
				},
			},
			args: args{
				rookVersion:     semver.MustParse("1.9.12"),
				name:            "my-store",
				level:           1,
				cephcluster:     nil,
				doFullReconcile: true,
			},
			want:      true,
			wantLevel: 1,
			wantErr:   false,
		},
		{
			name: "objectstore replication increase from 1 to 3, rook version 1.9.12",
			rookResources: []runtime.Object{
				&cephv1.CephObjectStore{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "ceph.rook.io/v1",
						Kind:       "CephObjectStore",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "my-store",
						Namespace: "rook-ceph",
					},
					Spec: cephv1.ObjectStoreSpec{
						MetadataPool: cephv1.PoolSpec{
							Replicated: cephv1.ReplicatedSpec{
								Size: 1,
							},
						},
						DataPool: cephv1.PoolSpec{
							Replicated: cephv1.ReplicatedSpec{
								Size: 1,
							},
						},
					},
				},
			},
			args: args{
				rookVersion:     semver.MustParse("1.9.12"),
				name:            "my-store",
				level:           3,
				cephcluster:     nil,
				doFullReconcile: false,
			},
			want:      true,
			wantLevel: 3,
			wantErr:   false,
		},
		// TODO: rookVersion 1.0.4
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rookClientset := rookfake.NewSimpleClientset(tt.rookResources...)
			c := &Controller{
				Config: ControllerConfig{
					CephV1: rookClientset.CephV1(),
				},
				Log: logger.NewDiscardLogger(),
			}
			got, err := c.SetObjectStoreReplication(tt.args.rookVersion, tt.args.name, tt.args.level, tt.args.cephcluster, tt.args.doFullReconcile)
			if (err != nil) != tt.wantErr {
				t.Errorf("Controller.SetObjectStoreReplication() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("Controller.SetObjectStoreReplication() = %v, want %v", got, tt.want)
			}
			cephOS, err := rookClientset.CephV1().CephObjectStores("rook-ceph").Get(context.Background(), "my-store", metav1.GetOptions{})
			if err != nil {
				t.Errorf("CephObjectStores.Get(\"my-store\") error = %v", err)
				return
			}
			if cephOS.Spec.MetadataPool.Replicated.Size != tt.wantLevel {
				t.Errorf("CephObjectStore.Spec.MetadataPool.Replicated.Size = %d, want %d", cephOS.Spec.MetadataPool.Replicated.Size, tt.args.level)
			}
			if cephOS.Spec.DataPool.Replicated.Size != tt.wantLevel {
				t.Errorf("CephObjectStore.Spec.DataPool.Replicated.Size = %d, want %d", cephOS.Spec.DataPool.Replicated.Size, tt.args.level)
			}
		})
	}
}
