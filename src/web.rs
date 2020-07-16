use super::{api, backend, models};
use rocket::fairing::AdHoc;
use rocket::http::{Cookie, Cookies, SameSite};
use rocket::request::Form;
use rocket::response::Redirect;
use rocket_contrib::json::Json;
use rocket_contrib::serve::StaticFiles;
use rocket_contrib::templates::Template;
use rocket_oauth2::{OAuth2, TokenResponse};
use serde::Serialize;

struct OAuthWikimedia;

#[database("toolsdb")]
pub struct ToolsDb(diesel::MysqlConnection);

/// root.html
#[derive(Serialize)]
struct IndexTemplate {
    title: String,
    username: Option<String>,
    bookmarklet: String,
}

/// syntactic helper to get the token out of cookies
fn extract_token(mut cookies: Cookies) -> Option<String> {
    match cookies.get_private("token") {
        Some(token) => Some(token.value().to_string()),
        None => None,
    }
}

#[get("/")]
async fn index(cookies: Cookies<'_>) -> Template {
    let username = if let Some(token) = extract_token(cookies) {
        api::get_username(&token).await
    } else {
        None
    };
    Template::render(
        "root",
        IndexTemplate {
            title: "Suggestor".to_string(),
            username,
            bookmarklet: "".to_string(),
        },
    )
}

/// Redirect to start OAuth2 flow
#[get("/login")]
fn oauth_login(
    oauth2: OAuth2<OAuthWikimedia>,
    mut cookies: Cookies<'_>,
) -> Redirect {
    oauth2.get_redirect(&mut cookies, &[]).unwrap()
}

/// Redirect target to get and save the access token
#[get("/auth")]
fn oauth_auth(
    token: TokenResponse<OAuthWikimedia>,
    mut cookies: Cookies<'_>,
) -> Redirect {
    cookies.add_private(
        Cookie::build("token", token.access_token().to_string())
            .same_site(SameSite::Lax)
            .finish(),
    );
    Redirect::to("/")
}

#[derive(FromForm)]
struct EditForm {
    wiki: String,
    text: String,
    summary: String,
    baserevid: u32,
    pageid: u32,
    pagename: String,
}

#[derive(Serialize)]
struct ApiResponse {
    id: u32,
    error: Option<String>,
}

/// Primary endpoint for data submission
#[post("/api", data = "<form>")]
fn api_endpoint(conn: ToolsDb, form: Form<EditForm>) -> Json<ApiResponse> {
    let new_edit = models::NewEdit {
        wiki: &form.wiki,
        text: form.text.as_bytes(),
        summary: &form.summary,
        baserevid: &form.baserevid,
        pageid: &form.pageid,
        pagename: &form.pagename,
        state: "pending",
    };
    // FIXME: validate data input is sane
    match backend::insert_edit(new_edit, &*conn) {
        Ok(edit) => Json(ApiResponse {
            id: edit.id,
            error: None,
        }),
        Err(e) => Json(ApiResponse {
            id: 0,
            error: Some(e.to_string()),
        }),
    }
}

/// pending.html
#[derive(Serialize)]
struct PendingTemplate {
    title: String,
    pendings: Vec<models::Edit>,
}

#[get("/pending")]
fn pending(conn: ToolsDb) -> Template {
    // TODO: error handling
    let pendings = backend::load_edits_with_state("pending", &*conn).unwrap();
    Template::render(
        "pending",
        PendingTemplate {
            pendings,
            title: "Pending edits".to_string(),
        },
    )
}

/// diff.html
#[derive(Serialize)]
struct DiffTemplate {
    title: String,
    edit: models::Edit,
    diff: String,
}

#[get("/diff/<edit_id>")]
async fn diff(edit_id: u32, conn: ToolsDb) -> Template {
    // TODO: error template if doesn't exist
    let edit = backend::load_edit(edit_id, &*conn).unwrap();
    // TODO: cache in redis?
    let diff = api::get_diff(&edit).await;
    Template::render(
        "diff",
        DiffTemplate {
            title: format!("Diff for proposed edit #{}", edit_id),
            edit,
            diff,
        },
    )
}

#[derive(FromForm)]
struct StatusForm {
    new_state: String,
}

/// status.html
#[derive(Serialize)]
struct StatusTemplate {
    title: String,
    error: Option<String>,
}

fn build_status(error: Option<String>) -> Template {
    Template::render(
        "status",
        StatusTemplate {
            title: "Updating status...".to_string(),
            error,
        },
    )
}

#[post("/status/<edit_id>", data = "<form>")]
async fn status(
    edit_id: u32,
    conn: ToolsDb,
    form: Form<StatusForm>,
    cookies: Cookies<'_>,
) -> Result<Template, Redirect> {
    // FIXME: csrf protection
    let token = match extract_token(cookies) {
        Some(token) => token,
        // Not logged in...send through flow again
        None => return Err(Redirect::to("/login")),
    };
    let edit = backend::load_edit(edit_id, &*conn);
    if edit.is_none() {
        return Ok(build_status(Some("invalid edit id provided".to_string())));
    }
    let new_state = match form.new_state.as_str() {
        "Approve" => "published",
        "Reject" => "rejected",
        _ => {
            return Ok(build_status(Some("invalid state provided".to_string())))
        }
    };
    if new_state == "published" {
        // If we're supposed to publish it, try to make the edit
        let resp = api::make_edit(edit.unwrap(), &token).await;
        if let Err(e) = resp {
            return Ok(build_status(Some(e.to_string())));
        }
    }
    // Update the database status
    Ok(match backend::set_state(edit_id, new_state, &*conn) {
        Ok(_) => build_status(None),
        Err(e) => build_status(Some(e.to_string())),
    })
}

// Add `/healthz` endpoint
rocket_healthz::healthz!();

pub fn rocket() -> rocket::Rocket {
    rocket::ignite()
        .attach(Template::fairing())
        .attach(ToolsDb::fairing())
        .attach(AdHoc::on_attach(
            "database migrations",
            super::run_db_migrations,
        ))
        .attach(OAuth2::<OAuthWikimedia>::fairing("wikimedia"))
        .mount(
            "/",
            routes![
                index,
                oauth_login,
                oauth_auth,
                api_endpoint,
                pending,
                diff,
                status,
                rocket_healthz
            ],
        )
        .mount(
            "/public",
            StaticFiles::from(concat!(env!("CARGO_MANIFEST_DIR"), "/public")),
        )
}
