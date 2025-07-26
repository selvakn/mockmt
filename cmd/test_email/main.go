package main

import (
	"bufio"
	"fmt"
	"net/smtp"
	"os"
	"strings"
)

func main() {
	fmt.Println("📧 SMTP Test Script")
	fmt.Println("Make sure the SMTP server is running on localhost:25")
	fmt.Println()

	// Get recipient email
	reader := bufio.NewReader(os.Stdin)
	fmt.Print("Enter recipient email (e.g., user@localhost): ")
	toEmail, _ := reader.ReadString('\n')
	toEmail = strings.TrimSpace(toEmail)
	if toEmail == "" {
		toEmail = "test@localhost"
	}

	// Email content
	subject := "Test Email from WebMail"
	body := `Hello!

This is a test email sent to your WebMail inbox.

Features of this email:
- Plain text content
- HTML formatting
- Sent via SMTP server
- Stored in SQLite database

Best regards,
WebMail Test System`

	htmlBody := `<!DOCTYPE html>
<html>
<head>
    <style>
        body { font-family: Arial, sans-serif; line-height: 1.6; color: #333; }
        .header { background-color: #4f46e5; color: white; padding: 20px; text-align: center; }
        .content { padding: 20px; }
        .footer { background-color: #f3f4f6; padding: 20px; text-align: center; color: #666; }
        .feature { background-color: #f0f9ff; padding: 10px; margin: 10px 0; border-left: 4px solid #0ea5e9; }
    </style>
</head>
<body>
    <div class="header">
        <h1>🎉 Welcome to WebMail!</h1>
    </div>
    
    <div class="content">
        <h2>Test Email Successfully Received</h2>
        <p>This is a test email sent to your WebMail inbox.</p>
        
        <h3>Features of this email:</h3>
        <div class="feature">✅ Plain text content</div>
        <div class="feature">✅ HTML formatting</div>
        <div class="feature">✅ Sent via SMTP server</div>
        <div class="feature">✅ Stored in SQLite database</div>
        
        <p>You can now view this email in your WebMail interface!</p>
    </div>
    
    <div class="footer">
        <p>Best regards,<br>WebMail Test System</p>
    </div>
</body>
</html>`

	fmt.Printf("\n📤 Sending test email to: %s\n", toEmail)
	fmt.Printf("📝 Subject: %s\n", subject)
	fmt.Println()

	// Send email
	if err := sendTestEmail(toEmail, subject, body, htmlBody); err != nil {
		fmt.Printf("❌ Failed to send email: %v\n", err)
		return
	}

	fmt.Printf("\n🎉 Test completed! Check your WebMail inbox for the email.\n")
	fmt.Printf("   Login to the web interface and look for emails to: %s\n", toEmail)
}

func sendTestEmail(toEmail, subject, body, htmlBody string) error {
	// Create email message
	message := fmt.Sprintf("From: test@example.com\r\n"+
		"To: %s\r\n"+
		"Subject: %s\r\n"+
		"MIME-Version: 1.0\r\n"+
		"Content-Type: multipart/alternative; boundary=boundary\r\n"+
		"\r\n"+
		"--boundary\r\n"+
		"Content-Type: text/plain; charset=UTF-8\r\n"+
		"\r\n"+
		"%s\r\n"+
		"\r\n"+
		"--boundary\r\n"+
		"Content-Type: text/html; charset=UTF-8\r\n"+
		"\r\n"+
		"%s\r\n"+
		"\r\n"+
		"--boundary--\r\n",
		toEmail, subject, body, htmlBody)

	// Send email
	err := smtp.SendMail("localhost:1025", nil, "test@example.com", []string{toEmail}, []byte(message))
	if err != nil {
		return err
	}

	fmt.Printf("✅ Email sent successfully to %s\n", toEmail)
	return nil
}
