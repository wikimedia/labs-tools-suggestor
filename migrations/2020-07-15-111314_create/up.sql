CREATE TABLE edits (
    id int unsigned NOT NULL PRIMARY KEY AUTO_INCREMENT,
    wiki VARCHAR(40) NOT NULL, -- domain name, e.g. en.wikipedia.org
    text BLOB NOT NULL, -- proposed wikitext
    summary VARCHAR(120) NOT NULL, -- edit summary
    baserevid int unsigned NOT NULL, -- rev id edit was made on top of
    pageid int unsigned NOT NULL, -- page id
    pagename VARCHAR(200) NOT NULL, -- page name
    state VARCHAR(10) NOT NULL -- "published", "rejected", "pending"
)
