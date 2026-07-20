package domain

// Post is the feed service's durable content model.
type Post struct {
	ID       int64  `json:"id"`
	AuthorID int64  `json:"author_id"`
	Body     string `json:"body"`
}

// Profile is returned by the profile service.
type Profile struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	Bio      string `json:"bio"`
}

// Recommendations is returned by the recommendation service.
type Recommendations struct {
	UserID int64    `json:"user_id"`
	Items  []string `json:"items"`
}

// Feed combines durable posts with independently degradable downstream data.
type Feed struct {
	User            Profile         `json:"user"`
	Posts           []Post          `json:"posts"`
	Recommendations Recommendations `json:"recommendations"`
	CacheHit        bool            `json:"cache_hit"`
}
