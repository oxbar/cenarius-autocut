package planner

import "strings"

// conceptAnchors are the visual words a stock result MUST relate to for a
// concept. Stock APIs (Pexels, Pixabay) rank by popularity as much as by
// meaning, so "technology" happily returns sunsets and VR headsets; a
// candidate whose description/tags/slug share no anchor is off-topic and is
// rejected by the asset resolver. Kept separate from conceptLexicon so the
// positional Concept literals stay untouched.
var conceptAnchors = map[string][]string{
	"artificial intelligence": {"artificial", "intelligence", "ai", "robot", "neural", "machine", "learning", "chatbot", "algorithm", "data", "futuristic", "digital", "brain"},
	"chatgpt":                 {"chatgpt", "chatbot", "openai", "ai", "chat", "assistant", "smartphone", "laptop", "screen"},
	"openai":                  {"openai", "chatgpt", "ai", "artificial", "chatbot"},
	"claude ai":               {"claude", "anthropic", "ai", "chatbot", "assistant"},
	"google gemini":           {"gemini", "google", "ai", "chatbot", "assistant"},
	"software development":    {"code", "coding", "programming", "programmer", "developer", "software", "laptop", "computer", "keyboard", "typing", "screen", "monitor", "html", "script"},
	"software developer":      {"developer", "programmer", "coding", "code", "programming", "laptop", "computer", "keyboard", "typing", "desk", "office", "engineer"},
	"technology":              {"technology", "computer", "laptop", "circuit", "electronic", "digital", "server", "data", "chip", "hardware", "screen", "tech"},
	"digital world":           {"network", "internet", "digital", "global", "connection", "fiber", "data", "globe", "cyber", "online"},
	"social media":            {"social", "media", "smartphone", "phone", "app", "instagram", "scrolling", "feed", "influencer", "like"},
	"instagram":               {"instagram", "social", "smartphone", "phone", "influencer", "app"},
	"youtube":                 {"youtube", "video", "creator", "camera", "vlog", "streaming", "filming"},
	"tiktok":                  {"tiktok", "smartphone", "phone", "vertical", "dance", "social", "creator"},
	"twitter x":               {"twitter", "social", "smartphone", "phone", "tweet", "app"},
	"linkedin":                {"linkedin", "business", "professional", "office", "career", "networking", "laptop"},
	"github":                  {"github", "git", "code", "coding", "programming", "developer", "repository", "laptop"},
	"java programming":        {"java", "code", "coding", "programming", "programmer", "developer", "laptop", "computer", "screen"},
	"spring framework":        {"spring", "java", "code", "coding", "programming", "developer", "screen"},
	"python programming":      {"python", "code", "coding", "programming", "developer", "screen", "laptop"},
	"javascript":              {"javascript", "web", "code", "coding", "programming", "developer", "screen", "html", "browser"},
	"docker containers":       {"docker", "container", "containers", "shipping", "port", "server", "cloud"},
	"kubernetes":              {"kubernetes", "server", "cluster", "cloud", "container", "datacenter", "rack"},
	"cloud computing":         {"cloud", "server", "datacenter", "data", "rack", "network", "computing"},
	"servers":                 {"server", "servers", "rack", "datacenter", "data", "cable", "network", "room"},
	"data":                    {"data", "database", "server", "chart", "analytics", "dashboard", "disk", "storage"},
	"cybersecurity":           {"security", "hacker", "cyber", "lock", "padlock", "password", "code", "hacking", "privacy", "shield"},
	"bug error":               {"error", "bug", "broken", "warning", "crash", "screen", "glitch", "code", "insect", "alert"},
	"bank finance":            {"bank", "card", "credit", "payment", "money", "finance", "banking", "atm", "wallet"},
	"money":                   {"money", "cash", "banknote", "banknotes", "coins", "dollar", "real", "payment", "finance", "wallet", "counting"},
	"company office":          {"office", "company", "business", "building", "workplace", "team", "desk", "corporate", "startup"},
	"business meeting":        {"meeting", "business", "team", "office", "conference", "discussion", "colleagues", "boss", "presentation"},
	"job career":              {"job", "career", "interview", "office", "work", "business", "handshake", "resume", "hiring", "employee"},
	"studying":                {"study", "studying", "student", "book", "books", "university", "learning", "class", "notebook", "library"},
	"english language":        {"english", "language", "travel", "airport", "world", "map", "dictionary", "abroad", "passport"},
	"smartphone":              {"smartphone", "phone", "mobile", "iphone", "android", "app", "screen", "hand", "cellphone"},
	"computer":                {"computer", "laptop", "pc", "desktop", "keyboard", "monitor", "screen", "desk"},
	"video recording":         {"camera", "video", "filming", "recording", "creator", "vlog", "tripod", "smartphone", "studio"},
	"apple":                   {"apple", "iphone", "macbook", "mac", "ipad"},
	"google":                  {"google", "search", "android", "browser"},
	"microsoft":               {"microsoft", "windows", "computer", "laptop"},
	"meta facebook":           {"meta", "facebook", "whatsapp", "social", "smartphone"},
	"amazon":                  {"amazon", "warehouse", "package", "delivery", "box", "shopping"},
	"football world cup":      {"football", "soccer", "stadium", "ball", "goal", "fans", "match", "player"},
	"brazil":                  {"brazil", "brazilian", "rio", "paulo", "flag", "brasilia"},
	"time clock":              {"clock", "time", "watch", "hourglass", "deadline", "night", "calendar"},
}

// ConceptAnchors returns the anchors of a lexicon concept, or nil.
func ConceptAnchors(name string) []string {
	return append([]string(nil), conceptAnchors[strings.ToLower(strings.TrimSpace(name))]...)
}
