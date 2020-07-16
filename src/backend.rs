use super::models::{Edit, NewEdit};
use super::schema;
use anyhow::Result;
use diesel::prelude::*;

/// Load an edit from the db
pub fn load_edit(edit_id: u32, conn: &MysqlConnection) -> Option<Edit> {
    use schema::edits::dsl::*;
    let edit = edits.filter(id.eq(edit_id)).first(conn);
    match edit {
        Ok(edit) => Some(edit),
        Err(_) => None,
    }
}

/// Update an edit to a new state
pub fn set_state(
    edit_id: u32,
    new_state: &str,
    conn: &MysqlConnection,
) -> Result<()> {
    use schema::edits::dsl::*;
    diesel::update(edits.filter(id.eq(edit_id)))
        .set(state.eq(new_state))
        .execute(conn)?;
    Ok(())
}

/// Get a vec of edits with the specified state
pub fn load_edits_with_state(
    edit_state: &str,
    conn: &MysqlConnection,
) -> Result<Vec<Edit>> {
    use schema::edits::dsl::*;
    Ok(edits
        .filter(state.eq(edit_state))
        .order(id.desc())
        .load(conn)?)
}

/// Save a new edit into the db
pub fn insert_edit(new_edit: NewEdit, conn: &MysqlConnection) -> Result<Edit> {
    match diesel::insert_into(schema::edits::table)
        .values(&new_edit)
        .execute(conn)
    {
        Ok(_) => {
            use schema::edits::dsl::*;
            Ok(edits.order(id.desc()).first(&*conn).unwrap())
        }
        Err(e) => Err(e.into()),
    }
}
