package main

import (
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/garyburd/redigo/redis"
	"github.com/mrjones/oauth"
)

type Config struct {
	Debug          bool
	Address        string
	Redis          string
	ConsumerKey    string `toml:"consumer_key"`
	ConsumerSecret string `toml:"consumer_secret"`
	WikiUrl        string `toml:"wiki_url"`
}

type Context struct {
	conf      *Config
	consumer  *oauth.Consumer
	pool      *redis.Pool
	templates map[string]*template.Template
}

func NewContext(conf *Config, consumer *oauth.Consumer, pool *redis.Pool) Context {
	templates := make(map[string]*template.Template)
	return Context{conf, consumer, pool, templates}
}

func (c *Context) Root(w http.ResponseWriter, r *http.Request) {
	err := c.templates["root"].ExecuteTemplate(w, "root.html", struct {
		Title string
	}{
		"Root",
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

var initiateParams = map[string]string{
	"title": "Special:OAuth/initiate",
}

func (c *Context) Initiate(w http.ResponseWriter, r *http.Request) {
	requestToken, url, err := c.consumer.GetRequestTokenAndUrlWithParams("oob", initiateParams)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	exp := time.Now().Add(time.Hour)
	val := fmt.Sprintf("%s:%s", requestToken.Token, requestToken.Secret)
	cookie := http.Cookie{Name: "oauthreqtoken", Value: val, Expires: exp}
	http.SetCookie(w, &cookie)
	http.Redirect(w, r, url, 303)
}

var callbackParams = map[string]string{
	"title": "Special:OAuth/token",
}

func (c *Context) Callback(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("oauthreqtoken")
	if err != nil {
		http.Error(w, "Token not found.", http.StatusBadRequest)
		return
	}
	vals := strings.SplitN(cookie.Value, ":", 2)
	requestToken := &oauth.RequestToken{
		Token:  vals[0],
		Secret: vals[1],
	}
	verificationCode := r.URL.Query().Get("oauth_verifier")
	accessToken, err := c.consumer.AuthorizeTokenWithParams(requestToken, verificationCode, callbackParams)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	err = c.templates["callback"].ExecuteTemplate(w, "callback.html", struct {
		Title       string
		AccessToken string
	}{
		"Callback",
		accessToken.Token,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

type Post struct {
	Wikitext  string `json:"wikitext"`
	ArticleId int    `json:"articleId"`
	Timestamp int
}

func (c *Context) Post(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if r.Body == nil {
		http.Error(w, "No body posted.", http.StatusBadRequest)
		return
	}
	var p Post
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		str := fmt.Sprintf("Cannot decode posted json: %s", err.Error())
		http.Error(w, str, http.StatusBadRequest)
		return
	}
	defer r.Body.Close()
	conn := c.pool.Get()
	defer conn.Close()
	key := "page:" + strconv.Itoa(p.ArticleId)
	conn.Send("MULTI")
	conn.Send("HMSET", key,
		"wikitext", p.Wikitext,
		"timestamp", strconv.FormatInt(time.Now().UTC().Unix(), 10),
		"id", p.ArticleId,
	)
	conn.Send("LPUSH", "pages", p.ArticleId)
	_, err := conn.Do("EXEC")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
	mar, err := json.Marshal(struct {
		Status string
	}{
		"success",
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(mar)
}

func (c *Context) Pending(w http.ResponseWriter, r *http.Request) {
	err := c.templates["pending"].ExecuteTemplate(w, "pending.html", struct {
		Title   string
		Pending []Post
	}{
		"Pending edits",
		c.ListPendingEdits(),
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

}

func (c *Context) ListPendingEdits() []Post {
	conn := c.pool.Get()
	defer conn.Close()
	values, err := redis.Values(conn.Do("SORT", "pages",
		"BY", "page:*->timestamp",
		"GET", "page:*->wikitext",
		"GET", "page:*->timestamp",
		"GET", "page:*->id",
	))
	if err != nil {
		panic(err)
	}

	var pending []Post
	for len(values) > 0 {
		var wikitext string
		var timestamp int
		var id int
		values, err = redis.Scan(values, &wikitext, &timestamp, &id)
		pending = append(pending, Post{wikitext, id, timestamp})
	}

	return pending
}

func (c *Context) CompileTemplates(templates []string) {
	base := "public/"
	layout := template.Must(template.ParseFiles(path.Join(base, "layout.html")))
	for _, t := range templates {
		clone, err := layout.Clone()
		if err != nil {
			log.Fatal(err)
		}
		c.templates[t] = template.Must(clone.ParseFiles(path.Join(base, fmt.Sprintf("%s.html", t))))
	}
}

func main() {
	var conf Config
	if _, err := toml.DecodeFile("config.toml", &conf); err != nil {
		log.Fatal("Couldn't decode config file.")
	}

	consumer := oauth.NewConsumer(conf.ConsumerKey, conf.ConsumerSecret, oauth.ServiceProvider{
		RequestTokenUrl:   conf.WikiUrl + "/index.php/Special:OAuth/initiate",
		AuthorizeTokenUrl: conf.WikiUrl + "/index.php/Special:OAuth/authorize",
		AccessTokenUrl:    conf.WikiUrl + "/index.php/Special:OAuth/token",
	})
	consumer.Debug(conf.Debug)

	pool := redis.Pool{
		MaxIdle:     3,
		IdleTimeout: 240 * time.Second,
		Dial: func() (redis.Conn, error) {
			c, err := redis.Dial("tcp", conf.Redis)
			if err != nil {
				return nil, err
			}
			// if _, err := c.Do("AUTH", password); err != nil {
			// 	c.Close()
			// 	return nil, err
			// }
			return c, nil
		},
		TestOnBorrow: func(c redis.Conn, t time.Time) error {
			if time.Since(t) < time.Minute {
				return nil
			}
			_, err := c.Do("PING")
			return err
		},
	}

	context := NewContext(&conf, consumer, &pool)
	context.CompileTemplates([]string{"root", "callback", "pending"})

	http.HandleFunc("/", context.Root)
	http.HandleFunc("/initiate", context.Initiate)
	http.HandleFunc("/callback", context.Callback)
	http.HandleFunc("/post", context.Post)
	http.HandleFunc("/pending", context.Pending)

	address := conf.Address
	// Prefer, for tools labs
	if port := os.Getenv("PORT"); len(port) > 0 {
		address = fmt.Sprintf(":%s", port)
	}

	log.Println("Listening on", address)
	log.Fatal(http.ListenAndServe(address, nil))
}
