package fargate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"

	"github.com/Elmanuel1/agentd-worker/internal/ids"
)

// ensureFamily returns the family's revision for the current config, registering one when the
// family has none or its latest was made from another config: a new image, size, handoff URL or
// CA then reaches the session's next launch. It runs the revision's own ARN, since ECS reads
// "the latest ACTIVE revision" through an eventually consistent model.
func (l *Launcher) ensureFamily(ctx context.Context, session ids.SessionID, accessPoint string) (string, error) {
	def := l.taskDefinition(session, accessPoint)
	raw, err := json.Marshal(def)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	config := hex.EncodeToString(sum[:16])
	active, err := l.revisions(ctx, session, ecstypes.TaskDefinitionStatusActive)
	if err != nil {
		return "", err
	}
	if len(active) == 0 {
		return l.register(ctx, def, config)
	}
	latest := active[len(active)-1]
	out, err := l.ecs.DescribeTaskDefinition(ctx, &ecs.DescribeTaskDefinitionInput{
		TaskDefinition: aws.String(latest),
		Include:        []ecstypes.TaskDefinitionField{ecstypes.TaskDefinitionFieldTags},
	})
	if err != nil {
		return "", fmt.Errorf("reading the task definition: %w", err)
	}
	if slices.ContainsFunc(out.Tags, func(t ecstypes.Tag) bool {
		return aws.ToString(t.Key) == tagConfig && aws.ToString(t.Value) == config
	}) {
		return latest, nil
	}
	return l.register(ctx, def, config)
}

func (l *Launcher) register(ctx context.Context, def *ecs.RegisterTaskDefinitionInput, config string) (string, error) {
	def.Tags = []ecstypes.Tag{{Key: aws.String(tagConfig), Value: aws.String(config)}}
	out, err := l.ecs.RegisterTaskDefinition(ctx, def)
	if err != nil {
		return "", fmt.Errorf("registering the task definition: %w", err)
	}
	return aws.ToString(out.TaskDefinition.TaskDefinitionArn), nil
}

// revisions lists the family's revisions in a status, oldest first. The prefix also matches
// longer session ids, so only ARNs ending in family:revision count.
func (l *Launcher) revisions(ctx context.Context, session ids.SessionID, status ecstypes.TaskDefinitionStatus) ([]string, error) {
	var arns []string
	in := &ecs.ListTaskDefinitionsInput{FamilyPrefix: aws.String(family(session)), Status: status}
	for p := ecs.NewListTaskDefinitionsPaginator(l.ecs, in); p.HasMorePages(); {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("listing the family's revisions: %w", err)
		}
		for _, arn := range page.TaskDefinitionArns {
			if strings.HasPrefix(path.Base(arn), family(session)+":") {
				arns = append(arns, arn)
			}
		}
	}
	return arns, nil
}

// deleteFamily deregisters the family's active revisions and deletes them with any left inactive
// by an earlier Release that stopped between the two steps.
func (l *Launcher) deleteFamily(ctx context.Context, session ids.SessionID) error {
	active, err := l.revisions(ctx, session, ecstypes.TaskDefinitionStatusActive)
	if err != nil {
		return err
	}
	for _, arn := range active {
		if _, err := l.ecs.DeregisterTaskDefinition(ctx, &ecs.DeregisterTaskDefinitionInput{TaskDefinition: aws.String(arn)}); err != nil {
			return fmt.Errorf("deregistering %s: %w", arn, err)
		}
	}
	inactive, err := l.revisions(ctx, session, ecstypes.TaskDefinitionStatusInactive)
	if err != nil {
		return err
	}
	for chunk := range slices.Chunk(inactive, 10) {
		out, err := l.ecs.DeleteTaskDefinitions(ctx, &ecs.DeleteTaskDefinitionsInput{TaskDefinitions: chunk})
		if err != nil {
			return fmt.Errorf("deleting the family's revisions: %w", err)
		}
		if len(out.Failures) > 0 {
			return fmt.Errorf("deleting the family's revisions: %s", aws.ToString(out.Failures[0].Reason))
		}
	}
	return nil
}
