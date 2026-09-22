package handlers

import (
	"context"

	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/linesmerrill/police-cad-api/databases"
)

// DepartmentMembershipForTest exposes getDepartmentMembership to the external
// test package. The members endpoints read a department's members array through
// it to decide who is already in the department and who is waiting on a join
// request, and both halves are worth testing directly.
type DepartmentMembershipResult struct {
	ApprovedIDs []primitive.ObjectID
	Requests    map[string]string
}

func DepartmentMembershipForTest(commDB databases.CommunityDatabase, communityID, departmentID string) DepartmentMembershipResult {
	m := getDepartmentMembership(context.Background(), commDB, communityID, departmentID)
	return DepartmentMembershipResult{ApprovedIDs: m.ApprovedIDs, Requests: m.Requests}
}
