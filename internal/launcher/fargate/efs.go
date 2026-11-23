package fargate

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/efs"
	efstypes "github.com/aws/aws-sdk-go-v2/service/efs/types"

	"github.com/Elmanuel1/agentd-worker/internal/ids"
)

// accessPointWait bounds the wait for a new access point to become available.
const accessPointWait = 30 * time.Second

// accessPoint returns the session's access point, creating it with the session id as its client
// token, and waits until it is available, since a new one starts out creating. A repeat of the
// token answers AccessPointAlreadyExists with the existing one's id.
func (l *Launcher) accessPoint(ctx context.Context, session ids.SessionID) (string, error) {
	out, err := l.efs.CreateAccessPoint(ctx, &efs.CreateAccessPointInput{
		ClientToken:  aws.String(string(session)),
		FileSystemId: aws.String(l.cfg.FileSystemID),
		PosixUser:    &efstypes.PosixUser{Uid: aws.Int64(l.cfg.WorkerUID), Gid: aws.Int64(l.cfg.WorkerGID)},
		RootDirectory: &efstypes.RootDirectory{
			Path:         aws.String("/" + sessionDir(session)),
			CreationInfo: &efstypes.CreationInfo{OwnerUid: aws.Int64(l.cfg.WorkerUID), OwnerGid: aws.Int64(l.cfg.WorkerGID), Permissions: aws.String("0750")},
		},
		Tags: []efstypes.Tag{{Key: aws.String(tagSession), Value: aws.String(string(session))}},
	})
	if exists, ok := errors.AsType[*efstypes.AccessPointAlreadyExists](err); ok {
		out, err = &efs.CreateAccessPointOutput{AccessPointId: exists.AccessPointId}, nil
	}
	if err != nil {
		return "", fmt.Errorf("creating the access point: %w", err)
	}
	id := aws.ToString(out.AccessPointId)
	deadline := time.Now().Add(accessPointWait)
	for {
		got, err := l.efs.DescribeAccessPoints(ctx, &efs.DescribeAccessPointsInput{AccessPointId: aws.String(id)})
		if err != nil {
			return "", fmt.Errorf("reading access point %s: %w", id, err)
		}
		if len(got.AccessPoints) == 1 && got.AccessPoints[0].LifeCycleState == efstypes.LifeCycleStateAvailable {
			return id, nil
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("access point %s is not available after %s", id, accessPointWait)
		}
		t := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			t.Stop()
			return "", ctx.Err()
		case <-t.C:
		}
	}
}

// accessPoints returns the file system's access points by the session each is tagged with.
func (l *Launcher) accessPoints(ctx context.Context) (map[ids.SessionID][]string, error) {
	out := map[ids.SessionID][]string{}
	in := &efs.DescribeAccessPointsInput{FileSystemId: aws.String(l.cfg.FileSystemID)}
	for p := efs.NewDescribeAccessPointsPaginator(l.efs, in); p.HasMorePages(); {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("listing access points: %w", err)
		}
		for _, ap := range page.AccessPoints {
			for _, tag := range ap.Tags {
				if aws.ToString(tag.Key) != tagSession {
					continue
				}
				if s, err := ids.ParseSessionID(aws.ToString(tag.Value)); err == nil {
					out[s] = append(out[s], aws.ToString(ap.AccessPointId))
				}
			}
		}
	}
	return out, nil
}
