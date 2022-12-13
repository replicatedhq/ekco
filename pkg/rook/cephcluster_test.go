package rook

import (
	"reflect"
	"testing"

	"github.com/blang/semver"
	cephv1 "github.com/rook/rook/pkg/apis/ceph.rook.io/v1"
)

func TestGetCephVersion(t *testing.T) {
	type args struct {
		cluster cephv1.CephCluster
	}
	tests := []struct {
		name    string
		args    args
		want    semver.Version
		wantErr bool
	}{
		{
			name: "status",
			args: args{
				cluster: cephv1.CephCluster{
					Spec: cephv1.ClusterSpec{
						CephVersion: cephv1.CephVersionSpec{
							Image: "ceph/ceph:v15.2.8-20201217",
						},
					},
					Status: cephv1.ClusterStatus{
						CephVersion: &cephv1.ClusterVersion{
							Image:   "ceph/ceph:v15.2.8-20201217",
							Version: "15.2.8-0",
						},
					},
				},
			},
			want:    semver.MustParse("15.2.8-0"),
			wantErr: false,
		},
		{
			name: "no status",
			args: args{
				cluster: cephv1.CephCluster{
					Spec: cephv1.ClusterSpec{
						CephVersion: cephv1.CephVersionSpec{
							Image: "kurlsh/ceph:v14.2.0-9065b09-20210625",
						},
					},
					Status: cephv1.ClusterStatus{},
				},
			},
			want:    semver.MustParse("14.2.0-0"),
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := GetCephVersion(tt.args.cluster)
			if (err != nil) != tt.wantErr {
				t.Errorf("GetCephVersion() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("GetCephVersion() = %v, want %v", got, tt.want)
			}
		})
	}
}
