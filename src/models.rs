use super::schema::edits;
use serde::Serialize;

#[derive(Queryable, Clone, Debug, Serialize)]
pub struct Edit {
    pub id: u32,
    pub wiki: String,
    pub text: Vec<u8>,
    pub summary: String,
    pub baserevid: u32,
    pub pageid: u32,
    pub pagename: String,
    pub state: String,
}

#[derive(Insertable)]
#[table_name = "edits"]
pub struct NewEdit<'a> {
    pub wiki: &'a str,
    pub text: &'a [u8],
    pub summary: &'a str,
    pub baserevid: &'a u32,
    pub pageid: &'a u32,
    pub pagename: &'a str,
    pub state: &'a str,
}
