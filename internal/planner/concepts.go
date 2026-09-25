package planner

// Concept is a visualizable idea. Aliases are matched on whole words of the
// Portuguese transcript; Queries are concrete, searchable English
// descriptions tried in order by the asset resolver (never a single query).
type Concept struct {
	Name     string   // english concept used in edit-plan / attribution
	Label    string   // short pt-BR label used by procedural motion graphics
	Aliases  []string // pt-BR/en words or phrases (whole-word match)
	Queries  []string // candidate searches, most specific first
	Layout   string   // preferred layout
	Type     string   // broll | card
	Salience float64  // 0..1 how concrete/visual the concept is
}

// conceptLexicon is intentionally concrete. Generic words ("coisa", "hoje")
// never become B-roll; entities and tangible concepts do.
var conceptLexicon = []Concept{
	{"artificial intelligence", "INTELIGÊNCIA ARTIFICIAL", []string{"inteligência artificial", "inteligencia artificial", "a ia", "da ia", "de ia", "com ia", "uma ia", "as ias", "ias generativas", "ia generativa"},
		[]string{"artificial intelligence neural network", "machine learning computer", "robot artificial intelligence", "data center servers"}, LayoutReaction, TypeBroll, 0.85},
	{"chatgpt", "CHATGPT", []string{"chatgpt", "chat gpt", "gpt"}, []string{"ChatGPT", "OpenAI chatbot", "chatbot conversation screen"}, LayoutCard, TypeCard, 0.95},
	{"openai", "OPENAI", []string{"openai", "open ai"}, []string{"OpenAI", "OpenAI logo", "artificial intelligence company"}, LayoutCard, TypeCard, 0.95},
	{"claude ai", "CLAUDE", []string{"claude", "anthropic"}, []string{"Anthropic Claude", "Anthropic", "chatbot conversation screen"}, LayoutCard, TypeCard, 0.9},
	{"google gemini", "GEMINI", []string{"gemini"}, []string{"Google Gemini", "Google artificial intelligence", "chatbot conversation screen"}, LayoutCard, TypeCard, 0.9},
	{"software development", "PROGRAMAÇÃO", []string{"programação", "programacao", "programar", "programando", "código", "codigo", "codar", "software", "desenvolvimento de software"},
		[]string{"software developer programming computer", "programmer coding", "source code on computer screen", "computer programming"}, LayoutReaction, TypeBroll, 0.85},
	{"software developer", "DEV", []string{"programador", "programadora", "programadores", "desenvolvedor", "desenvolvedores", "dev", "devs"},
		[]string{"software developer working laptop", "programmer at desk", "people working computers office"}, LayoutReaction, TypeBroll, 0.8},
	{"technology", "TECNOLOGIA", []string{"tecnologia", "tecnologias", "tech"},
		[]string{"computer technology", "circuit board electronics", "data center servers", "laptop computer desk"}, LayoutReaction, TypeBroll, 0.65},
	{"digital world", "MUNDO DIGITAL", []string{"mundo digital", "internet", "online", "rede", "redes"},
		[]string{"internet network cables", "global network earth night lights", "fiber optic cables"}, LayoutFullscreen, TypeBroll, 0.7},
	{"social media", "REDES SOCIAIS", []string{"redes sociais", "rede social"}, []string{"social media smartphone", "smartphone apps screen", "person using smartphone"}, LayoutReaction, TypeBroll, 0.8},
	{"instagram", "INSTAGRAM", []string{"instagram", "insta", "reels"}, []string{"Instagram", "smartphone social media app", "person using smartphone"}, LayoutCard, TypeCard, 0.9},
	{"youtube", "YOUTUBE", []string{"youtube", "shorts"}, []string{"YouTube", "video streaming screen", "content creator filming"}, LayoutCard, TypeCard, 0.9},
	{"tiktok", "TIKTOK", []string{"tiktok", "tik tok"}, []string{"TikTok", "smartphone vertical video", "person using smartphone"}, LayoutCard, TypeCard, 0.9},
	{"twitter x", "X / TWITTER", []string{"twitter", "no x", "do x", "no twitter"}, []string{"Twitter", "social media smartphone", "smartphone apps screen"}, LayoutCard, TypeCard, 0.85},
	{"linkedin", "LINKEDIN", []string{"linkedin"}, []string{"LinkedIn", "business networking laptop", "office work laptop"}, LayoutCard, TypeCard, 0.9},
	{"github", "GITHUB", []string{"github", "git hub"}, []string{"GitHub", "Git version control", "source code on computer screen"}, LayoutCard, TypeCard, 0.9},
	{"java programming", "JAVA", []string{"java"}, []string{"Java programming language", "Java source code", "source code on computer screen"}, LayoutReaction, TypeBroll, 0.85},
	{"spring framework", "SPRING BOOT", []string{"spring boot", "spring"}, []string{"Spring Framework", "Java source code", "source code on computer screen"}, LayoutReaction, TypeBroll, 0.8},
	{"python programming", "PYTHON", []string{"python"}, []string{"Python programming language", "Python source code", "source code on computer screen"}, LayoutReaction, TypeBroll, 0.85},
	{"javascript", "JAVASCRIPT", []string{"javascript", "typescript", "front end", "frontend", "front-end"}, []string{"JavaScript source code", "web development code", "source code on computer screen"}, LayoutReaction, TypeBroll, 0.8},
	{"docker containers", "DOCKER", []string{"docker", "container", "containers"}, []string{"Docker software", "shipping containers port", "data center servers"}, LayoutReaction, TypeBroll, 0.8},
	{"kubernetes", "KUBERNETES", []string{"kubernetes", "k8s"}, []string{"Kubernetes", "data center servers", "server rack"}, LayoutReaction, TypeBroll, 0.8},
	{"cloud computing", "CLOUD", []string{"nuvem", "cloud", "aws", "azure", "gcp"}, []string{"data center servers", "server rack", "cloud computing"}, LayoutReaction, TypeBroll, 0.8},
	{"servers", "SERVIDORES", []string{"servidor", "servidores", "produção", "producao", "deploy"}, []string{"server rack", "data center servers", "network cables server"}, LayoutReaction, TypeBroll, 0.75},
	{"data", "DADOS", []string{"dados", "banco de dados", "database"}, []string{"data center servers", "hard disk drive", "database server"}, LayoutReaction, TypeBroll, 0.7},
	{"cybersecurity", "SEGURANÇA", []string{"segurança", "seguranca", "hacker", "hackers", "vazamento", "senha", "senhas"}, []string{"computer security lock", "hacker computer", "padlock keyboard"}, LayoutFullscreen, TypeBroll, 0.85},
	{"bug error", "BUG", []string{"bug", "bugs", "erro", "erros", "quebrou", "caiu"}, []string{"computer error screen", "blue screen of death", "insect bug macro"}, LayoutFullscreen, TypeBroll, 0.8},
	{"bank finance", "BANCO", []string{"banco", "nubank", "itaú", "itau", "bradesco", "fintech", "cartão", "cartao"}, []string{"bank cards", "credit card payment", "banking app smartphone"}, LayoutReaction, TypeBroll, 0.85},
	{"money", "DINHEIRO", []string{"dinheiro", "salário", "salario", "grana", "reais", "dólar", "dolar", "milhões", "milhoes", "bilhões", "bilhoes"}, []string{"brazilian real banknotes", "money banknotes", "counting money"}, LayoutReaction, TypeBroll, 0.85},
	{"company office", "EMPRESA", []string{"empresa", "empresas", "startup", "startups", "big tech", "escritório", "escritorio"}, []string{"office building", "people working office", "business meeting office"}, LayoutReaction, TypeBroll, 0.75},
	{"business meeting", "REUNIÃO", []string{"reunião", "reuniao", "reuniões", "gestor", "chefe", "ceo", "diretor"}, []string{"business meeting", "office meeting table", "business people discussion"}, LayoutReaction, TypeBroll, 0.8},
	{"job career", "CARREIRA", []string{"vaga", "vagas", "emprego", "carreira", "entrevista", "contratado", "demitido", "demissão", "layoff"}, []string{"job interview", "office workers", "business people handshake"}, LayoutReaction, TypeBroll, 0.8},
	{"studying", "ESTUDO", []string{"estudar", "estudando", "faculdade", "curso", "cursos", "aprender"}, []string{"student studying books", "university lecture hall", "person studying laptop"}, LayoutReaction, TypeBroll, 0.75},
	{"english language", "INGLÊS", []string{"inglês", "ingles", "gringa", "exterior", "internacional"}, []string{"world map", "airport travel", "english dictionary"}, LayoutReaction, TypeBroll, 0.7},
	{"smartphone", "CELULAR", []string{"celular", "smartphone", "iphone", "android", "aplicativo", "app"}, []string{"smartphone in hand", "person using smartphone", "iPhone"}, LayoutReaction, TypeBroll, 0.85},
	{"computer", "COMPUTADOR", []string{"computador", "notebook", "laptop", "pc", "máquina", "maquina"}, []string{"laptop computer desk", "personal computer", "computer keyboard typing"}, LayoutReaction, TypeBroll, 0.8},
	{"video recording", "GRAVAÇÃO", []string{"gravação", "gravacao", "gravando", "vídeo teste", "video teste", "câmera", "camera", "creator"}, []string{"video camera filming", "content creator filming", "camera recording video"}, LayoutReaction, TypeBroll, 0.75},
	{"apple", "APPLE", []string{"apple", "macbook", "mac"}, []string{"Apple Inc.", "MacBook", "Apple store"}, LayoutCard, TypeCard, 0.85},
	{"google", "GOOGLE", []string{"google"}, []string{"Google", "Google office", "Google search"}, LayoutCard, TypeCard, 0.85},
	{"microsoft", "MICROSOFT", []string{"microsoft", "windows"}, []string{"Microsoft", "Microsoft office building", "Windows computer"}, LayoutCard, TypeCard, 0.85},
	{"meta facebook", "META", []string{"meta", "facebook", "whatsapp", "zuckerberg"}, []string{"Meta Platforms", "Facebook", "smartphone social media app"}, LayoutCard, TypeCard, 0.85},
	{"amazon", "AMAZON", []string{"amazon"}, []string{"Amazon company", "Amazon warehouse", "data center servers"}, LayoutCard, TypeCard, 0.85},
	{"football world cup", "COPA DO MUNDO", []string{"copa", "copa do mundo", "futebol", "seleção", "selecao", "gol"}, []string{"FIFA World Cup", "football stadium", "soccer ball stadium"}, LayoutFullscreen, TypeBroll, 0.85},
	{"brazil", "BRASIL", []string{"brasil", "brasileiro", "brasileiros"}, []string{"Brazil flag", "Brasília", "São Paulo skyline"}, LayoutReaction, TypeBroll, 0.7},
	{"time clock", "TEMPO", []string{"prazo", "deadline", "sexta-feira", "sexta feira", "madrugada"}, []string{"clock time", "office at night", "wall clock"}, LayoutReaction, TypeBroll, 0.65},
}

// ConceptByName returns a lexicon concept (used by tests and the Ollama
// validator to enrich queries).
func ConceptByName(name string) (Concept, bool) {
	n := normalizeWord(name)
	for _, c := range conceptLexicon {
		if normalizeWord(c.Name) == n {
			return c, true
		}
	}
	return Concept{}, false
}
