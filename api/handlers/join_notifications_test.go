package handlers

import "testing"

// Production survey (Aug 2026): 10,973 memberships sit at "pending" and 3,441 at
// "declined", and 2,899 users hold nothing but pending memberships. None of those
// people were ever told the outcome — both resolution handlers cleared the
// admins' join_request notification and sent the requester nothing.

func TestBuildJoinResolvedNotification(t *testing.T) {
	tests := []struct {
		name        string
		res         JoinResolution
		wantSend    bool
		wantType    string
		wantMessage string
	}{
		{
			name: "community approval names the community",
			res: JoinResolution{
				Status: "approved", PreviousStatus: "pending",
				RequesterID: "user1", CommunityID: "comm1", CommunityName: "Rockford RP",
			},
			wantSend: true, wantType: NotificationCommunityApproved,
			wantMessage: "You are now a member of Rockford RP",
		},
		{
			name: "community decline names the community",
			res: JoinResolution{
				Status: "declined", PreviousStatus: "pending",
				RequesterID: "user1", CommunityID: "comm1", CommunityName: "Rockford RP",
			},
			wantSend: true, wantType: NotificationCommunityDeclined,
			wantMessage: "Your request to join Rockford RP was declined",
		},
		{
			name: "department approval names both department and community",
			res: JoinResolution{
				Status: "approved", PreviousStatus: "pending",
				RequesterID: "user1", CommunityID: "comm1", CommunityName: "Rockford RP",
				DepartmentID: "dept1", DepartmentName: "State Police",
			},
			wantSend: true, wantType: NotificationDepartmentApproved,
			wantMessage: "You were approved for State Police in Rockford RP",
		},
		{
			name: "department decline names both",
			res: JoinResolution{
				Status: "declined", PreviousStatus: "pending",
				RequesterID: "user1", CommunityID: "comm1", CommunityName: "Rockford RP",
				DepartmentID: "dept1", DepartmentName: "State Police",
			},
			wantSend: true, wantType: NotificationDepartmentDeclined,
			wantMessage: "Your request to join State Police in Rockford RP was declined",
		},
		{
			// An admin double-clicking approve must not tell a long-standing member
			// they have just been let in.
			name: "no change means no notification",
			res: JoinResolution{
				Status: "approved", PreviousStatus: "approved",
				RequesterID: "user1", CommunityID: "comm1", CommunityName: "Rockford RP",
			},
			wantSend: false,
		},
		{
			name: "a status that is not a resolution stays quiet",
			res: JoinResolution{
				Status: "banned", PreviousStatus: "approved",
				RequesterID: "user1", CommunityID: "comm1", CommunityName: "Rockford RP",
			},
			wantSend: false,
		},
		{
			name: "pending is not a resolution",
			res: JoinResolution{
				Status: "pending", PreviousStatus: "",
				RequesterID: "user1", CommunityID: "comm1", CommunityName: "Rockford RP",
			},
			wantSend: false,
		},
		{
			name:     "missing requester is not addressable",
			res:      JoinResolution{Status: "approved", PreviousStatus: "pending", CommunityID: "comm1"},
			wantSend: false,
		},
		{
			name:     "missing community would produce a message about nothing",
			res:      JoinResolution{Status: "approved", PreviousStatus: "pending", RequesterID: "user1"},
			wantSend: false,
		},
		{
			// 148,515 communities carry no visibility and some carry no name; the
			// copy still has to read like a sentence.
			name: "unnamed community falls back to a readable phrase",
			res: JoinResolution{
				Status: "approved", PreviousStatus: "pending",
				RequesterID: "user1", CommunityID: "comm1",
			},
			wantSend: true, wantType: NotificationCommunityApproved,
			wantMessage: "You are now a member of the community",
		},
		{
			name: "unnamed department falls back too",
			res: JoinResolution{
				Status: "approved", PreviousStatus: "pending",
				RequesterID: "user1", CommunityID: "comm1", CommunityName: "Rockford RP",
				DepartmentID: "dept1",
			},
			wantSend: true, wantType: NotificationDepartmentApproved,
			wantMessage: "You were approved for the department in Rockford RP",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := BuildJoinResolvedNotification(tt.res)
			if ok != tt.wantSend {
				t.Fatalf("send = %v, want %v", ok, tt.wantSend)
			}
			if !tt.wantSend {
				return
			}
			if got.Type != tt.wantType {
				t.Errorf("type = %q, want %q", got.Type, tt.wantType)
			}
			if got.Message != tt.wantMessage {
				t.Errorf("message = %q, want %q", got.Message, tt.wantMessage)
			}
			if got.SentToID != tt.res.RequesterID {
				t.Errorf("sentToID = %q, want the requester %q", got.SentToID, tt.res.RequesterID)
			}
			if got.Seen {
				t.Error("a new notification must not start seen")
			}
			if got.ID == "" {
				t.Error("notification needs an id")
			}
		})
	}
}

// The data slots must match the convention the join_request notifications use,
// because the clients and the resolution cleanup both key off data1/data3 to tell
// a community request from a department one.
func TestBuildJoinResolvedNotificationDataSlots(t *testing.T) {
	community, ok := BuildJoinResolvedNotification(JoinResolution{
		Status: "approved", PreviousStatus: "pending",
		RequesterID: "user1", ActorID: "admin1",
		CommunityID: "comm1", CommunityName: "Rockford RP",
	})
	if !ok {
		t.Fatal("expected a notification")
	}
	if community.Data1 != "comm1" {
		t.Errorf("data1 = %q, want the community id", community.Data1)
	}
	if community.Data3 != "" {
		t.Errorf("data3 = %q, want empty so this reads as community-level", community.Data3)
	}
	if community.SentFromID != "admin1" {
		t.Errorf("sentFromID = %q, want the resolving admin", community.SentFromID)
	}

	department, ok := BuildJoinResolvedNotification(JoinResolution{
		Status: "approved", PreviousStatus: "pending",
		RequesterID: "user1", CommunityID: "comm1", CommunityName: "Rockford RP",
		DepartmentID: "dept1", DepartmentName: "State Police",
	})
	if !ok {
		t.Fatal("expected a notification")
	}
	if department.Data3 != "dept1" {
		t.Errorf("data3 = %q, want the department id", department.Data3)
	}
	if department.Data4 != "State Police" {
		t.Errorf("data4 = %q, want the department name", department.Data4)
	}
}

func TestJoinResolutionIsDepartment(t *testing.T) {
	if (JoinResolution{}).IsDepartment() {
		t.Error("a resolution with no department id is community-level")
	}
	if !(JoinResolution{DepartmentID: "dept1"}).IsDepartment() {
		t.Error("a resolution carrying a department id is department-level")
	}
}
