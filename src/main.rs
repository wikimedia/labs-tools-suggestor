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
async fn run_db_migrations(mut rocket: Rocket) -> Result<Rocket, Rocket> {
    let conn = web::ToolsDb::get_one(rocket.inspect().await).unwrap();
    match embedded_migrations::run(&*conn) {
        Ok(()) => Ok(rocket),
        Err(_) => {
            println!("Failed to run migrations!");
            Err(rocket)
        }
    }
}

#[launch]
fn launch() -> Rocket {
    web::rocket()
}
