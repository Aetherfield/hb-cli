package browser

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestInspectCookiesReadsOnlyTheSchemeAndPersistence(t *testing.T) {
	p := filepath.Join(t.TempDir(), "Cookies")
	db, err := sql.Open("sqlite", p)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE cookies (host_key TEXT, name TEXT, value TEXT, encrypted_value BLOB, is_persistent INTEGER);
		INSERT INTO cookies VALUES ('hb.lafi.sh','hb_session','',X'7632300102',1);
		INSERT INTO cookies VALUES ('.lafi.sh','other','',X'7631300102',1);
		INSERT INTO cookies VALUES ('example.com','hb_session','',X'7631300102',0);`)
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	c, err := InspectCookies(p, []string{"hb.lafi.sh"}, []string{"hb_session"})
	if err != nil {
		t.Fatal(err)
	}
	if !c.Readable || c.Found != 1 || c.AppBound != 1 || c.Persistent != 1 {
		t.Fatalf("%+v", c)
	}
	c, _ = InspectCookies(p, []string{"hb.lafi.sh"}, nil)
	if c.Found != 2 {
		t.Fatalf("without a name list every cookie of the site counts: %+v", c)
	}
}
