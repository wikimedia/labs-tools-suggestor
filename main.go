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
	loggedIn := false
	_, err := r.Cookie("oauthtoken")
	if err == nil {
		loggedIn = true
	}
	err = c.templates["root"].ExecuteTemplate(w, "root.html", struct {
		Title    string
		LoggedIn bool
	}{
		"Root",
		loggedIn,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

func setToken(w http.ResponseWriter, name, token, secret string, duration time.Duration) {
	exp := time.Now().Add(duration)
	val := fmt.Sprintf("%s:%s", token, secret)
	cookie := &http.Cookie{Name: name, Value: val, Expires: exp}
	http.SetCookie(w, cookie)
}

func getToken(r *http.Request, name string) (string, string, error) {
	cookie, err := r.Cookie(name)
	if err != nil {
		return "", "", err
	}
	vals := strings.SplitN(cookie.Value, ":", 2)
	return vals[0], vals[1], nil
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
	setToken(w, "oauthreqtoken", requestToken.Token, requestToken.Secret, time.Hour)
	http.Redirect(w, r, url, 303)
}

var callbackParams = map[string]string{
	"title": "Special:OAuth/token",
}

func (c *Context) Callback(w http.ResponseWriter, r *http.Request) {
	token, secret, err := getToken(r, "oauthreqtoken")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	requestToken := &oauth.RequestToken{Token: token, Secret: secret}
	verificationCode := r.URL.Query().Get("oauth_verifier")
	accessToken, err := c.consumer.AuthorizeTokenWithParams(requestToken, verificationCode, callbackParams)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	setToken(w, "oauthtoken", accessToken.Token, accessToken.Secret, 30*24*time.Hour)
	http.Redirect(w, r, "/", 303)
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
		return
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
	list, err := c.ListPendingEdits()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	err = c.templates["pending"].ExecuteTemplate(w, "pending.html", struct {
		Title   string
		Pending []Post
	}{
		"Pending edits",
		list,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

func (c *Context) ListPendingEdits() ([]Post, error) {
	conn := c.pool.Get()
	defer conn.Close()

	values, err := redis.Values(conn.Do("SORT", "pages",
		"BY", "page:*->timestamp",
		"GET", "page:*->wikitext",
		"GET", "page:*->timestamp",
		"GET", "page:*->id",
	))
	if err != nil {
		return nil, err
	}

	var pending []Post
	for len(values) > 0 {
		var wikitext string
		var timestamp int
		var id int
		values, err = redis.Scan(values, &wikitext, &timestamp, &id)
		pending = append(pending, Post{wikitext, id, timestamp})
	}
	return pending, nil
}

type RespTokens struct {
	CSRFToken string `json:"csrftoken"`
}

type RespQuery struct {
	Tokens RespTokens
}

type Resp struct {
	Query RespQuery
}

func (c *Context) Approve(w http.ResponseWriter, r *http.Request) {
	token, secret, err := getToken(r, "oauthtoken")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	accessToken := &oauth.AccessToken{Token: token, Secret: secret}
	client, err := c.consumer.MakeHttpClient(accessToken)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	req, err := http.NewRequest("GET", c.conf.WikiUrl+"/api.php", nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	q := req.URL.Query()
	q.Add("action", "query")
	q.Add("meta", "tokens")
	q.Add("type", "csrf")
	q.Add("format", "json")
	req.URL.RawQuery = q.Encode()

	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if resp.Body == nil {
		http.Error(w, "No body got.", http.StatusBadRequest)
		return
	}
	var p Resp
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
		str := fmt.Sprintf("Cannot decode got json: %s", err.Error())
		http.Error(w, str, http.StatusBadRequest)
		return
	}
	defer resp.Body.Close()

	log.Println(p.Query.Tokens.CSRFToken)
	w.WriteHeader(http.StatusOK)
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
	http.HandleFunc("/approve", context.Approve)

	address := conf.Address
	// Prefer, for tools labs
	if port := os.Getenv("PORT"); len(port) > 0 {
		address = fmt.Sprintf(":%s", port)
	}

	log.Println("Listening on", address)
	log.Fatal(http.ListenAndServe(address, nil))
}
