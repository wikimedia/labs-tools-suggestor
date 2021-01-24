use super::{api, backend, models};
use rocket::fairing::AdHoc;
use rocket::http::{Cookie, CookieJar, SameSite};
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
fn extract_token(cookies: &CookieJar) -> Option<String> {
    match cookies.get_private("token") {
        Some(token) => Some(token.value().to_string()),
        None => None,
    }
}

#[get("/")]
async fn index(cookies: &CookieJar<'_>) -> Template {
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
    cookies: &CookieJar<'_>,
) -> Redirect {
    oauth2.get_redirect(&cookies, &[]).unwrap()
}

/// Redirect target to get and save the access token
#[get("/auth")]
fn oauth_auth(
    token: TokenResponse<OAuthWikimedia>,
    cookies: &CookieJar<'_>,
) -> Redirect {
    cookies.add_private(
        Cookie::build("token", token.access_token().to_string())
            .same_site(SameSite::Lax)
            .finish(),
    );
    Redirect::to("/")
}

#[derive(FromForm)]
pub struct EditForm {
    pub wiki: String,
    pub text: String,
    pub summary: String,
    pub baserevid: u32,
    pub pageid: u32,
    pub pagename: String,
}

#[derive(Serialize)]
struct ApiResponse {
    id: u32,
    error: Option<String>,
}

/// Primary endpoint for data submission
#[post("/api", data = "<edit_form>")]
async fn api_endpoint(
    conn: ToolsDb,
    edit_form: Form<EditForm>,
) -> Json<ApiResponse> {
    let form = edit_form.into_inner();
    match backend::insert_edit(form, &conn).await {
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
async fn pending(conn: ToolsDb) -> Template {
    // TODO: error handling
    let pendings = backend::load_edits_with_state("pending".to_string(), &conn)
        .await
        .unwrap();
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
    let edit = backend::load_edit(edit_id, &conn).await.unwrap();
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
    cookies: &CookieJar<'_>,
) -> Result<Template, Redirect> {
    // FIXME: csrf protection
    let token = match extract_token(cookies) {
        Some(token) => token,
        // Not logged in...send through flow again
        None => return Err(Redirect::to("/login")),
    };
    let edit = backend::load_edit(edit_id, &conn).await;
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
    Ok(
        match backend::set_state(edit_id, new_state.to_string(), &conn).await {
            Ok(_) => build_status(None),
            Err(e) => build_status(Some(e.to_string())),
        },
    )
}

// Add `/healthz` endpoint
// Disabled because causing SIGILL on `cargo test`
// rocket_healthz::healthz!();

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
                // healthz
            ],
        )
        .mount(
            "/public",
            StaticFiles::from(concat!(env!("CARGO_MANIFEST_DIR"), "/public")),
        )
}
