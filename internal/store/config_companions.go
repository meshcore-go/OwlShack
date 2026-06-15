package store

import "database/sql"

// Companion is a node personality the bot runs. id is the stable key; name,
// pubkey and private_key are all mutable (rename / rotate keypair freely).
type Companion struct {
	ID             int64
	Name           string
	PrivateKey     string
	PubKey         string
	Latitude       *float64
	Longitude      *float64
	AdvertInterval *int
}

type CompanionRepo struct{ db *sql.DB }

func scanCompanion(s interface{ Scan(...any) error }) (*Companion, error) {
	var c Companion
	if err := s.Scan(&c.ID, &c.Name, &c.PrivateKey, &c.PubKey, &c.Latitude, &c.Longitude, &c.AdvertInterval); err != nil {
		return nil, err
	}
	return &c, nil
}

func (r *CompanionRepo) List() ([]Companion, error) {
	rows, err := r.db.Query(`
		SELECT id, name, private_key, pubkey, latitude, longitude, advert_interval
		FROM companions ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Companion
	for rows.Next() {
		c, err := scanCompanion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

func (r *CompanionRepo) Get(id int64) (*Companion, error) {
	return scanCompanion(r.db.QueryRow(`
		SELECT id, name, private_key, pubkey, latitude, longitude, advert_interval
		FROM companions WHERE id = ?`, id))
}

// IDByName resolves a companion's surrogate id from its (current) name. The API
// keeps name in its URLs; history is keyed by id, so handlers resolve here.
// Returns sql.ErrNoRows when no companion has that name.
func (r *CompanionRepo) IDByName(name string) (int64, error) {
	var id int64
	err := r.db.QueryRow(`SELECT id FROM companions WHERE name = ?`, name).Scan(&id)
	return id, err
}

// Create inserts a companion and sets c.ID to the new surrogate key.
func (r *CompanionRepo) Create(c *Companion) error {
	res, err := r.db.Exec(`
		INSERT INTO companions (name, private_key, pubkey, latitude, longitude, advert_interval)
		VALUES (?, ?, ?, ?, ?, ?)`,
		c.Name, c.PrivateKey, c.PubKey, c.Latitude, c.Longitude, c.AdvertInterval)
	if err != nil {
		return err
	}
	c.ID, err = res.LastInsertId()
	return err
}

func (r *CompanionRepo) Update(c *Companion) error {
	_, err := r.db.Exec(`
		UPDATE companions SET name=?, private_key=?, pubkey=?, latitude=?, longitude=?, advert_interval=?
		WHERE id=?`,
		c.Name, c.PrivateKey, c.PubKey, c.Latitude, c.Longitude, c.AdvertInterval, c.ID)
	return err
}

func (r *CompanionRepo) Delete(id int64) error {
	_, err := r.db.Exec(`DELETE FROM companions WHERE id = ?`, id)
	return err
}

// CompanionChannel is a companion-owned channel. private_key empty = a public /
// hashtag-derived channel. Triggers reference these rows via trigger_channels.
type CompanionChannel struct {
	ID          int64
	CompanionID int64
	Name        string
	PrivateKey  string
}

type ChannelRepo struct{ db *sql.DB }

// List returns every channel across all companions.
func (r *ChannelRepo) List() ([]CompanionChannel, error) {
	rows, err := r.db.Query(`
		SELECT id, companion_id, name, private_key
		FROM companion_channels ORDER BY companion_id ASC, id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CompanionChannel
	for rows.Next() {
		var c CompanionChannel
		if err := rows.Scan(&c.ID, &c.CompanionID, &c.Name, &c.PrivateKey); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r *ChannelRepo) ListByCompanion(companionID int64) ([]CompanionChannel, error) {
	rows, err := r.db.Query(`
		SELECT id, companion_id, name, private_key
		FROM companion_channels WHERE companion_id = ? ORDER BY id ASC`, companionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CompanionChannel
	for rows.Next() {
		var c CompanionChannel
		if err := rows.Scan(&c.ID, &c.CompanionID, &c.Name, &c.PrivateKey); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r *ChannelRepo) Get(id int64) (*CompanionChannel, error) {
	var c CompanionChannel
	err := r.db.QueryRow(`
		SELECT id, companion_id, name, private_key FROM companion_channels WHERE id = ?`, id).
		Scan(&c.ID, &c.CompanionID, &c.Name, &c.PrivateKey)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (r *ChannelRepo) Create(c *CompanionChannel) error {
	res, err := r.db.Exec(`
		INSERT INTO companion_channels (companion_id, name, private_key) VALUES (?, ?, ?)`,
		c.CompanionID, c.Name, c.PrivateKey)
	if err != nil {
		return err
	}
	c.ID, err = res.LastInsertId()
	return err
}

func (r *ChannelRepo) Update(c *CompanionChannel) error {
	_, err := r.db.Exec(`
		UPDATE companion_channels SET name=?, private_key=? WHERE id=?`,
		c.Name, c.PrivateKey, c.ID)
	return err
}

func (r *ChannelRepo) Delete(id int64) error {
	_, err := r.db.Exec(`DELETE FROM companion_channels WHERE id = ?`, id)
	return err
}
