package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// AdminUser represents an admin user in the system
type AdminUser struct {
	ID        primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	Email     string             `bson:"email" json:"email"`
	Password  string             `bson:"password" json:"-"`
	Role      string             `bson:"role" json:"role"` // Legacy field for backward compatibility
	Roles     []string           `bson:"roles" json:"roles"` // New array-based roles
	Active    bool               `bson:"active" json:"active"`
	CreatedAt time.Time          `bson:"createdAt" json:"createdAt"`
	CreatedBy string             `bson:"createdBy" json:"createdBy"`
	LastLoginAt *time.Time       `bson:"lastLoginAt,omitempty" json:"lastLoginAt,omitempty"`
}

// CreateAdminUserRequest represents the request to create a new admin user
type CreateAdminUserRequest struct {
	Email string `json:"email" validate:"required,email"`
	Role  string `json:"role" validate:"required,oneof=admin owner"`
}

// CreateAdminUserResponse represents the response when creating a new admin user
type CreateAdminUserResponse struct {
	Success   bool       `json:"success"`
	Message   string     `json:"message"`
	AdminUser AdminUser  `json:"adminUser"`
	ResetLink string     `json:"resetLink"`
}

// SendAdminResetEmailRequest represents the request to send a password reset email
type SendAdminResetEmailRequest struct {
	Email string `json:"email" validate:"required,email"`
}

// SendAdminResetEmailResponse represents the response when sending a password reset email
type SendAdminResetEmailResponse struct {
	Success   bool   `json:"success"`
	Message   string `json:"message"`
	EmailSent bool   `json:"emailSent"`
}

// ErrorResponse represents a standardized error response
type ErrorResponse struct {
	Success bool   `json:"success"`
	Error   string `json:"error"`
	Code    string `json:"code"`
}

// AdminSearchRequest represents the request to search for admins
type AdminSearchRequest struct {
	Query string `json:"query" validate:"required"`
}

// AdminSearchResponse represents the response when searching for admins
type AdminSearchResponse struct {
	Success bool           `json:"success"`
	Admins  []AdminUser   `json:"admins"`
	Total   int           `json:"total"`
}

// AdminDetailsResponse represents the response when getting admin details
type AdminDetailsResponse struct {
	Success bool      `json:"success"`
	Admin   AdminUser `json:"admin"`
}

// ChangeRoleRequest represents a request to change an admin's role
type ChangeRoleRequest struct {
	Role string `json:"role" validate:"required,oneof=admin owner"`
}

// ChangeRolesRequest represents a request to change an admin's roles
type ChangeRolesRequest struct {
	Roles []string `json:"roles" validate:"required,min=1,dive,oneof=admin owner"`
}

// UpdateAdminLastLoginRequest represents a request to update admin's last login time
type UpdateAdminLastLoginRequest struct {
	LastLoginAt time.Time `json:"lastLoginAt"`
}

// ChangeRoleResponse represents the response when changing an admin's role
type ChangeRoleResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Admin   AdminUser `json:"admin"`
}

// DeleteAdminResponse represents the response when deleting an admin
type DeleteAdminResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// AdminActivityData represents admin activity tracking data for responses
type AdminActivityData struct {
	TotalLogins    int            `json:"totalLogins"`
	PasswordResets int            `json:"passwordResets"`
	TempPasswords  int            `json:"tempPasswords"`
	AvgSessionTime string         `json:"avgSessionTime"` // e.g., "45m"
	ChartData      []ChartDataPoint `json:"chartData"`
	RecentActivity []ActivityItem   `json:"recentActivity"`
}

// ChartDataPoint represents a single data point for activity charts
type ChartDataPoint struct {
	Date  string `json:"date"`  // e.g., "Jan 19", "2025-01-19"
	Value int    `json:"value"` // number of logins
}

// ActivityItem represents a single admin activity event
type ActivityItem struct {
	Type      string    `json:"type"`      // login, logout, password_reset, etc.
	Title     string    `json:"title"`     // "Admin logged in"
	Details   string    `json:"details"`   // "IP: 192.168.1.1"
	Timestamp time.Time `json:"timestamp"`
}

// AdminActivityResponse represents the response when getting admin activity
type AdminActivityResponse struct {
	Success  bool              `json:"success"`
	Activity AdminActivityData `json:"activity"`
}

// AdminLoginActivity represents a login/logout event
type AdminLoginActivity struct {
	ID           primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	AdminID      primitive.ObjectID `bson:"adminId" json:"adminId"`
	Type         string             `bson:"type" json:"type"` // login, logout
	IP           string             `bson:"ip" json:"ip"`
	UserAgent    string             `bson:"userAgent" json:"userAgent"`
	Timestamp    time.Time          `bson:"timestamp" json:"timestamp"`
	SessionID    string             `bson:"sessionId,omitempty" json:"sessionId,omitempty"`
	Duration     *time.Duration     `bson:"duration,omitempty" json:"duration,omitempty"` // for logout events
}

// AdminActionActivity represents administrative actions
type AdminActionActivity struct {
	ID          primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	AdminID     primitive.ObjectID `bson:"adminId" json:"adminId"`
	ActionType  string             `bson:"actionType" json:"actionType"` // password_reset, temp_password, role_change, etc.
	TargetID    string             `bson:"targetId,omitempty" json:"targetId,omitempty"` // ID of affected user/community
	TargetType  string             `bson:"targetType,omitempty" json:"targetType,omitempty"` // user, community, admin
	Details     string             `bson:"details" json:"details"`
	Timestamp   time.Time          `bson:"timestamp" json:"timestamp"`
	IP          string             `bson:"ip" json:"ip"`
}

// AdminActivityStorage represents a single admin activity event for storage in database
type AdminActivityStorage struct {
	ID             primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	AdminID        string            `bson:"adminId" json:"adminId"`
	Type           string            `bson:"type" json:"type"`
	Title          string            `bson:"title" json:"title"`
	Details        string            `bson:"details" json:"details"`
	Timestamp      time.Time         `bson:"timestamp" json:"timestamp"`
	IP             string            `bson:"ip" json:"ip"`
	CreatedAt      time.Time         `bson:"createdAt" json:"createdAt"`
	SessionDuration string           `bson:"sessionDuration,omitempty" json:"sessionDuration,omitempty"` // For logout events
}

// AdminActivityLogRequest represents the request to log admin activity
type AdminActivityLogRequest struct {
	AdminID        string    `json:"adminId"`
	Type           string    `json:"type"`
	Title          string    `json:"title"`
	Details        string    `json:"details"`
	Timestamp      time.Time `json:"timestamp"`
	IP             string    `json:"ip"`
	SessionDuration string   `json:"sessionDuration,omitempty"` // Optional: session duration for logout events
	CurrentUser    map[string]interface{} `json:"currentUser,omitempty"` // For permission checking
}

// AdminPasswordReset stores password reset tokens for admin accounts
type AdminPasswordReset struct {
	ID        primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	AdminID   primitive.ObjectID `bson:"adminId" json:"adminId"`
	TokenHash string             `bson:"tokenHash" json:"-"`
	ExpiresAt interface{}        `bson:"expiresAt" json:"expiresAt"`
	UsedAt    interface{}        `bson:"usedAt,omitempty" json:"usedAt,omitempty"`
	CreatedAt interface{}        `bson:"createdAt" json:"createdAt"`
}

// Admin response models
type AdminUserResult struct {
	ID            string      `json:"id"`
	Email         string      `json:"email"`
	Username      string      `json:"username,omitempty"`
	IsDeactivated bool        `json:"isDeactivated"`
	CreatedAt     interface{} `json:"createdAt"`
	LastLoginAt   interface{} `json:"lastLoginAt,omitempty"`
}

type AdminCommunityResult struct {
	ID          string      `json:"id"`
	Name        string      `json:"name"`
	Visibility  string      `json:"visibility"`  // "public" or "private"
	CreatedAt   interface{} `json:"createdAt"`
	Owner       *OwnerInfo  `json:"owner,omitempty"`
	MemberCount int         `json:"memberCount"`
	DepartmentCount int      `json:"departmentCount"`
}

type OwnerInfo struct {
	ID       string `json:"id"`
	Email    string `json:"email"`
	Username string `json:"username,omitempty"`
}

type AdminUserDetails struct {
	ID                   string               `json:"id"`
	Email                string               `json:"email"`
	Username             string               `json:"username,omitempty"`
	IsDeactivated        bool                 `json:"isDeactivated"`
	CreatedAt            interface{}          `json:"createdAt"`
	LastLoginAt          interface{}          `json:"lastLoginAt,omitempty"`
	Communities          []AdminUserCommunity `json:"communities,omitempty"`
	CommunitiesCount     int                  `json:"communitiesCount"`
	ResetPasswordToken   string               `json:"resetPasswordToken,omitempty"`
	ResetPasswordExpires interface{}          `json:"resetPasswordExpires,omitempty"`
}

type AdminUserCommunity struct {
	ID         string      `json:"id"`
	Name       string      `json:"name"`
	Status     string      `json:"status"`
	Role       string      `json:"role"`
	Department string      `json:"department,omitempty"`
	JoinedAt   interface{} `json:"joinedAt,omitempty"`
}

type AdminCommunityDetails struct {
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	Visibility     string            `json:"visibility"`  // "public" or "private"
	CreatedAt      interface{}       `json:"createdAt"`
	Owner          *OwnerInfo        `json:"owner,omitempty"`
	MemberCount    int               `json:"memberCount"`
	Departments    []CommunityDept   `json:"departments,omitempty"`
	DepartmentCount int               `json:"departmentCount"`
	Subscription   *CommunitySubscription `json:"subscription,omitempty"`
}

type CommunityDept struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	MemberCount int    `json:"memberCount"`
}

type CommunitySubscription struct {
	Active           bool        `json:"active"`
	CreatedAt        interface{} `json:"createdAt"`
	DurationMonths   int         `json:"durationMonths"`
	ExpirationDate   interface{} `json:"expirationDate"`
	ID               string      `json:"id"`
	IsAnnual         bool        `json:"isAnnual"`
	Plan             string      `json:"plan"`
	PurchaseDate     interface{} `json:"purchaseDate"`
	UpdatedAt        interface{} `json:"updatedAt"`
}

type AdminPendingVerificationResult struct {
	ID        primitive.ObjectID `json:"id"`
	Email     string             `json:"email"`
	Code      string             `json:"code"`
	Attempts  int                `json:"attempts"`
	CreatedAt interface{}        `json:"createdAt"`
}


