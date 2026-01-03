# Context.Background() to Request Context Fixes - Checkpoint

## Summary

Fixed HTTP handlers to use request context with timeout (`api.WithQueryTimeout(r.Context())`) instead of `context.Background()` for proper trace tracking and timeout handling.

## Progress by File

### ✅ Completed Files (All HTTP handlers fixed)

1. **api/handlers/call.go** - ✅ **8 instances fixed** (0 remaining)
   - CreateCallHandler
   - UpdateCallByIDHandler
   - DeleteCallByIDHandler
   - AddCallNoteHandler
   - UpdateCallNoteByIDHandler
   - DeleteCallNoteByIDHandler

2. **api/handlers/civilian.go** - ✅ **3 instances fixed** (0 remaining)
   - DeleteCivilianHandler
   - DeleteCriminalHistoryHandler
   - UpdateCivilianApprovalStatusHandler

### 🔄 In Progress Files

3. **api/handlers/user.go** - ✅ **59 instances fixed** (24 remaining)
   - Fixed HTTP handlers: UserHandler, UsersFindAllHandler, FetchUsersByIdsHandler, UserLoginHandler, UserCreateHandler, UserCheckEmailHandler, UsersDiscoverPeopleHandler, AddFriendHandler, AddNotificationHandler, GetUserNotificationsHandler, MarkNotificationAsReadHandler, DeleteNotificationHandler, fetchUserFriendsByID, fetchFriendsAndMutualFriendsCount, GetUserCommunitiesHandler, UpdateFriendStatusHandler, RemoveCommunityFromUserHandler, BanUserFromCommunityHandler, UpdateUserByIDHandler, BlockUserHandler, UnblockUserHandler, SetOnlineStatusHandler, UnfriendUserHandler, AddUserToPendingDepartmentHandler, SubscribeUserHandler
   - Remaining 24 instances are mostly in helper functions (handleSubscriptionCreated, handleInvoicePaymentSucceeded, handleRevenueCatInitialPurchase, etc.) that need context passed as a parameter

4. **api/handlers/community.go** - ✅ **11 instances fixed** (43 remaining)
   - Fixed HTTP handlers: AddEventToCommunityHandler, UpdateEventByIDHandler, DeleteEventByIDHandler, UpdateCommunityByIDHandler, AddRoleToCommunityHandler, AddMembersToRoleHandler, UpdateRoleNameHandler, DeleteRoleFromCommunityHandler, UpdateRolePermissionsHandler, GetBannedUsersHandler
   - 43 instances remaining in other HTTP handlers

### 📊 Statistics

- **Total instances fixed in HTTP handlers: 81**
- **Total instances remaining in HTTP handlers: ~67** (43 in community.go, 24 in user.go helper functions)
- **Total instances remaining across all files: 197** (includes non-HTTP handlers, helper functions, test files, etc.)

## Pattern Applied

All fixes follow this pattern:

```go
// Before:
_, err = db.UpdateOne(context.Background(), filter, update)

// After:
// Use request context with timeout for proper trace tracking and timeout handling
ctx, cancel := api.WithQueryTimeout(r.Context())
defer cancel()

_, err = db.UpdateOne(ctx, filter, update)
```

## Files Not Yet Started

- api/handlers/template_migration.go (11 instances)
- api/handlers/ems.go (1 instance)
- api/handlers/firearm.go (4 instances)
- api/handlers/license.go (3 instances)
- api/handlers/announcement.go (23 instances)
- api/handlers/ems_medical_component.go (10 instances)
- api/handlers/component.go (8 instances)
- api/handlers/department_template.go (8 instances)
- api/handlers/template.go (10 instances)
- api/handlers/emspersona.go (5 instances)
- api/handlers/emsVehicle.go (5 instances)
- api/handlers/medication.go (5 instances)
- api/handlers/userpreferences.go (7 instances)
- api/handlers/invitecode.go (1 instance)
- api/handlers/report.go (1 instance)
- api/handlers/remove_community_test.go (28 instances - test file)

## Next Steps

1. Continue fixing remaining HTTP handlers in `community.go` (43 instances)
2. Fix helper functions in `user.go` that need context passed as parameter (24 instances)
3. Fix HTTP handlers in other handler files (announcement.go, template.go, etc.)

## Notes

- All linter errors are pre-existing warnings (struct literal unkeyed fields, deprecated functions) and not related to our changes
- The `context` import was removed from `call.go` and `civilian.go` as it's no longer needed
- Helper functions that don't have direct access to `r.Context()` will need to be refactored to accept context as a parameter

