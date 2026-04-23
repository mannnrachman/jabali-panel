package models

import "time"

// EmailAutoresponder represents a vacation/autoresponse for a mailbox.
// Stalwart integration: JMAP VacationResponse (RFC 8621 §8).
// One per mailbox; jabali is truth.
type EmailAutoresponder struct {
	MailboxID string    `gorm:"type:char(26);primaryKey" json:"mailbox_id"`
	Enabled   bool      `gorm:"type:tinyint(1);not null;default:0" json:"enabled"`
	FromDate  *time.Time `gorm:"type:datetime(6)" json:"from_date"`
	ToDate    *time.Time `gorm:"type:datetime(6)" json:"to_date"`
	Subject   *string   `gorm:"type:varchar(998)" json:"subject"`
	TextBody  *string   `gorm:"type:text" json:"text_body"`
	HTMLBody  *string   `gorm:"type:mediumtext" json:"html_body"`
	ManagedBy string    `gorm:"type:varchar(16);default:'m6.5'" json:"managed_by"`
	UpdatedAt time.Time `gorm:"type:datetime(6);not null" json:"updated_at"`
}

func (EmailAutoresponder) TableName() string { return "email_autoresponders" }
