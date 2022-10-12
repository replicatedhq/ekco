package cluster

import (
	"context"
	"testing"

	"github.com/blang/semver"
	"github.com/replicatedhq/ekco/pkg/logger"
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
					Client:      clientset,
					RookVersion: tt.rookVersion,
				},
				Log: logger.NewDiscardLogger(),
			}
			got, err := c.SetCephCSIResources(context.Background(), tt.args.nodeCount)
			if (err != nil) != tt.wantErr {
				t.Errorf("Controller.SetCephCSIResources() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("Controller.SetCephCSIResources() = %v, want %v", got, tt.want)
			}

			got, err = c.SetCephCSIResources(context.Background(), tt.args.nodeCount)
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
