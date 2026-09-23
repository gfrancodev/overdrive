package main

type Project struct {
	Root         string `json:"root"`
	Repository   string `json:"repository"`
	Organization string `json:"organization"`
	Module       string `json:"module,omitempty"`
}

type Memory struct {
	ID               string  `json:"id"`
	Kind             string  `json:"kind"`
	Scope            string  `json:"scope"`
	ScopeID          string  `json:"scope_id"`
	Subject          string  `json:"subject,omitempty"`
	Content          string  `json:"content"`
	PacketContent    string  `json:"packet_content,omitempty"`
	Confidence       float64 `json:"confidence"`
	Priority         int     `json:"priority"`
	Status           string  `json:"status"`
	Source           string  `json:"source"`
	SourceRef        string  `json:"source_ref,omitempty"`
	Evidence         string  `json:"evidence,omitempty"`
	SuccessCount     int     `json:"success_count"`
	FailureCount     int     `json:"failure_count"`
	EvidenceScore    float64 `json:"evidence_score,omitempty"`
	CreatedAt        string  `json:"created_at"`
	UpdatedAt        string  `json:"updated_at"`
	LastValidatedAt  string  `json:"last_validated_at,omitempty"`
	Score            float64 `json:"score,omitempty"`
	Origin           string  `json:"origin,omitempty"`
	PeerDeviceID     string  `json:"peer_device_id,omitempty"`
	CircleID         string  `json:"circle_id,omitempty"`
	Layer            int     `json:"layer,omitempty"`
	ProblemSignature string  `json:"problem_signature,omitempty"`
	SourceFolder     string  `json:"source_folder,omitempty"`
	VectorSpace      string  `json:"vector_space,omitempty"`
	HotIndex         bool    `json:"hot_index,omitempty"`
	VectorScore      float64 `json:"vector_score,omitempty"`
}

type JSONStore struct {
	Version  int      `json:"version"`
	Memories []Memory `json:"memories"`
}

type RecallResponse struct {
	Project       Project  `json:"project"`
	Memories      []Memory `json:"memories"`
	PeerMemories  []Memory `json:"peer_memories"`
	CriticalRules []Memory `json:"critical_rules"`
	WorkingMemory []Memory `json:"working_memory"`
	Backend       string   `json:"backend"`
}

type StatusResponse struct {
	Version     string `json:"version"`
	Home        string `json:"home"`
	Store       string `json:"store"`
	VectorIndex string `json:"vector_index"`
	MemoryCount int    `json:"memory_count"`
	Backend     string `json:"backend"`
	TurboVec    bool   `json:"turbovec_available"`
	Embedder    string `json:"embedder"`
	EmbedderDim int    `json:"embedder_dim"`
}

type LedgerEntry struct {
	ID             int64  `json:"id"`
	RunID          string `json:"run_id"`
	Decision       string `json:"decision"`
	Evidence       string `json:"evidence,omitempty"`
	Reason         string `json:"reason,omitempty"`
	Risk           string `json:"risk,omitempty"`
	Reversibility  string `json:"reversibility,omitempty"`
	CreatedAt      string `json:"created_at"`
}
