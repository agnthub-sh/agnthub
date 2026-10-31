// Package category is the agnthub.sh agent category taxonomy: the stable slugs
// publishers declare in agnthub.yaml and the registry uses to group Agents.
//
// The taxonomy grows over time. A slug's shape is fixed (ValidSlug), but a
// slug that this version does not know is not an error: readers keep newer
// slugs as declared and leave it to the registry to decide which it knows.
package category

import "strings"

// Version is the taxonomy revision this package describes.
const Version = 1

// MaxSlugLen bounds a category slug.
const MaxSlugLen = 64

// Category is one taxonomy entry.
type Category struct {
	// Slug is the stable identifier used in agnthub.yaml, APIs and URLs.
	Slug string
	// Name is the display name.
	Name string
	// Covers summarizes which Agents belong in the category.
	Covers string
}

var taxonomy = [...]Category{
	{
		"software-engineering",
		"Software Engineering",
		"general coding agents, implementers, pair programmers",
	},
	{"frontend-development", "Frontend Development", "web UI code, React/Vue/Svelte, CSS"},
	{
		"backend-development",
		"Backend Development",
		"servers, services, API implementation, frameworks (Rails, Laravel, Django, Spring, Go)",
	},
	{"mobile-development", "Mobile Development", "iOS, Android, React Native, Flutter"},
	{"game-development", "Game Development", "game engines, gameplay, Unity/Unreal/Godot"},
	{
		"software-architecture",
		"Software Architecture",
		"system design, architecture reviews, DDD, ADRs",
	},
	{"code-review", "Code Review", "reviewing diffs/PRs for correctness and quality"},
	{"testing-qa", "Testing & QA", "test writing, QA, test strategy, e2e"},
	{"debugging", "Debugging", "root-cause analysis, bug hunting, troubleshooting"},
	{"refactoring", "Refactoring", "legacy code modernization, cleanup, migrations"},
	{"devops-ci-cd", "DevOps & CI/CD", "pipelines, deployments, release engineering"},
	{"cloud-infrastructure", "Cloud Infrastructure", "AWS/GCP/Azure, Terraform, Kubernetes, IaC"},
	{"site-reliability", "Site Reliability", "monitoring, incidents, observability, on-call"},
	{"security", "Security", "security audits, threat modeling, pentesting, secure coding"},
	{"data-engineering", "Data Engineering", "pipelines, ETL, warehouses, dbt"},
	{"data-analysis", "Data Science & Analytics", "analysis, statistics, BI, notebooks"},
	{
		"machine-learning",
		"Machine Learning & AI",
		"ML/LLM engineering, prompt engineering, model training",
	},
	{"databases", "Databases", "SQL, schema design, query tuning, DBA"},
	{"api-design", "API Design", "API contracts, OpenAPI, GraphQL, SDK design"},
	{"documentation", "Documentation", "code/project docs, READMEs, docstrings"},
	{"technical-writing", "Technical Writing", "tutorials, guides, technical articles"},
	{"product-management", "Product Management", "PRDs, roadmaps, prioritization, user stories"},
	{"product-design", "Product Design", "end-to-end product design, design thinking"},
	{"ux-research", "UX Research", "user research, interviews, usability testing"},
	{"ui-design", "UI Design", "visual design, design systems, Figma"},
	{"accessibility", "Accessibility", "a11y audits, WCAG"},
	{
		"project-management",
		"Project Management",
		"planning, tracking, coordination, status reports",
	},
	{"agile-scrum", "Agile & Scrum", "sprint planning, retros, scrum master"},
	{"marketing", "Marketing", "marketing strategy, campaigns, growth"},
	{"seo", "SEO", "search optimization, keyword research"},
	{"content-marketing", "Content Marketing", "blogs, newsletters, content strategy"},
	{"social-media", "Social Media", "social posts, community, scheduling"},
	{"copywriting", "Copywriting", "ad copy, landing pages, product copy"},
	{"sales", "Sales", "prospecting, outreach, proposals, CRM"},
	{"customer-support", "Customer Support", "support tickets, help desk, FAQ"},
	{"finance", "Finance", "financial analysis, investing, budgeting, FP&A"},
	{"accounting", "Accounting", "bookkeeping, tax, invoices, reconciliation"},
	{"legal", "Legal", "contracts, legal research, legal drafting"},
	{"compliance-privacy", "Compliance & Privacy", "GDPR, SOC 2, policies, audits"},
	{"hr-recruiting", "HR & Recruiting", "hiring, interviews, job descriptions, people ops"},
	{"operations", "Operations", "business operations, processes, logistics"},
	{"business-strategy", "Business Strategy", "strategy, market analysis, startups, consulting"},
	{"research", "Research", "literature/web research, academic, deep research"},
	{"education", "Education & Tutoring", "teaching, tutoring, course creation, learning"},
	{"healthcare", "Healthcare", "medical, clinical, health informatics"},
	{"automation", "Automation", "workflow automation, scripting, bots, n8n/Zapier"},
	{"productivity", "Productivity", "personal productivity, notes, planning, email"},
	{"personal-assistant", "Personal Assistant", "general assistants, life admin"},
	{"translation-localization", "Translation & Localization", "translation, i18n, localization"},
	{"blockchain-web3", "Blockchain & Web3", "smart contracts, Solidity, crypto"},
	{"open-source", "Open Source Maintenance", "issue triage, changelogs, maintainer workflows"},
	{"performance", "Performance Optimization", "profiling, speed, efficiency"},
	{"writing-creative", "Creative Writing", "fiction, storytelling, scripts, poetry"},
}

var bySlug = func() map[string]int {
	index := make(map[string]int, len(taxonomy))
	for i, c := range taxonomy {
		index[c.Slug] = i
	}
	return index
}()

// All returns the taxonomy in display order. The slice is a copy.
func All() []Category {
	out := make([]Category, len(taxonomy))
	copy(out, taxonomy[:])
	return out
}

// Len reports the number of known categories.
func Len() int { return len(taxonomy) }

// Lookup returns the known category for slug.
func Lookup(slug string) (Category, bool) {
	i, ok := bySlug[slug]
	if !ok {
		return Category{}, false
	}
	return taxonomy[i], true
}

// Known reports whether slug is in this taxonomy version.
func Known(slug string) bool {
	_, ok := bySlug[slug]
	return ok
}

// Index returns slug's display position, or -1 when unknown.
func Index(slug string) int {
	i, ok := bySlug[slug]
	if !ok {
		return -1
	}
	return i
}

// ValidSlug reports whether s is slug-shaped: lowercase ASCII letters and
// digits in hyphen-separated words, at most MaxSlugLen bytes. Known and
// not-yet-known categories share this shape.
func ValidSlug(s string) bool {
	if s == "" || len(s) > MaxSlugLen || s[0] == '-' || s[len(s)-1] == '-' ||
		strings.Contains(s, "--") {
		return false
	}
	for i := range len(s) {
		c := s[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
			return false
		}
	}
	return true
}
