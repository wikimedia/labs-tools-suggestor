#[macro_use]
extern crate rocket;
#[macro_use]
extern crate rocket_contrib;
#[macro_use]
extern crate diesel;
#[macro_use]
extern crate diesel_migrations;

use rocket::Rocket;

mod api;
mod backend;
mod models;
mod schema;
mod web;

embed_migrations!();

/// Automatically create/update our schema at launch
async fn run_db_migrations(rocket: Rocket) -> Result<Rocket, Rocket> {
    web::ToolsDb::get_one(&rocket)
        .await
        .expect("database connection")
        .run(|c| match embedded_migrations::run(c) {
            Ok(()) => Ok(rocket),
            Err(e) => {
                error!("Failed to run database migrations: {:?}", e);
                Err(rocket)
            }
        })
        .await
}

#[launch]
fn launch() -> Rocket {
    web::rocket()
}
