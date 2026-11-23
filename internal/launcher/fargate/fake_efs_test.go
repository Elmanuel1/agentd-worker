package fargate

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/efs"
	efstypes "github.com/aws/aws-sdk-go-v2/service/efs/types"
)

// The fake's EFS.

func (f *fakeAWS) CreateAccessPoint(_ context.Context, in *efs.CreateAccessPointInput, _ ...func(*efs.Options)) (*efs.CreateAccessPointOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := "fsap-" + aws.ToString(in.ClientToken)
	if _, ok := f.points[id]; ok {
		return nil, &efstypes.AccessPointAlreadyExists{AccessPointId: aws.String(id)}
	}
	f.points[id] = efstypes.AccessPointDescription{AccessPointId: aws.String(id), Tags: in.Tags, RootDirectory: in.RootDirectory}
	return &efs.CreateAccessPointOutput{AccessPointId: aws.String(id)}, nil
}

func (f *fakeAWS) DescribeAccessPoints(_ context.Context, in *efs.DescribeAccessPointsInput, _ ...func(*efs.Options)) (*efs.DescribeAccessPointsOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := &efs.DescribeAccessPointsOutput{}
	for id, ap := range f.points {
		if in.AccessPointId == nil || aws.ToString(in.AccessPointId) == id {
			ap.LifeCycleState = efstypes.LifeCycleStateAvailable
			out.AccessPoints = append(out.AccessPoints, ap)
		}
	}
	return out, nil
}

func (f *fakeAWS) DeleteAccessPoint(_ context.Context, in *efs.DeleteAccessPointInput, _ ...func(*efs.Options)) (*efs.DeleteAccessPointOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.points, aws.ToString(in.AccessPointId))
	return &efs.DeleteAccessPointOutput{}, nil
}
