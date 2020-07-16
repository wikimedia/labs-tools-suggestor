Suggestor
=========

Tor users suggest edits for your approval.

## Contributing

* Install libmysqlclient-dev (or the MariaDB version)
* Install [rustup](https://rustup.rs/) if you don't have it already
* `git clone https://gerrit.wikimedia.org/r/labs/tools/suggestor && cd suggestor`
* Add OAuth and database keys to `Rocket.toml`
* `cargo run`

Use `cargo check` for fast analysis without rebuilding the whole project.
Run `cargo fmt` before committing.

## Testing

You can use something similar to the following to submit a test edit suggestion:
```python
import requests

req = requests.post('http://localhost:8000/api', data={
    'wiki': 'test.wikipedia.org',
    'text': 'Example edit',
    'summary': 'This is an edit I want to make',
    'baserevid': 274801,
    'pageid': 90777,
    'pagename': 'User:Legoktm/sandbox',
})
print(req.json())
req.raise_for_status()
```
