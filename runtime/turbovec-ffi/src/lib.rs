use std::ffi::CStr;
use std::os::raw::{c_char, c_float, c_int, c_ulonglong, c_void};
use std::path::Path;
use turbovec::IdMapIndex;

struct Handle {
    index: IdMapIndex,
    path: String,
    dim: usize,
}

fn read_cstr(ptr: *const c_char) -> Option<String> {
    if ptr.is_null() {
        return None;
    }
    unsafe { CStr::from_ptr(ptr).to_str().ok().map(|s| s.to_string()) }
}

#[no_mangle]
pub extern "C" fn od_tv_open(path: *const c_char, dim: c_int, bit_width: c_int) -> *mut c_void {
    let Some(path_str) = read_cstr(path) else {
        return std::ptr::null_mut();
    };
    if dim <= 0 || bit_width <= 0 {
        return std::ptr::null_mut();
    }
    let dim = dim as usize;
    let bit_width = bit_width as usize;
    let index = if Path::new(&path_str).exists() {
        IdMapIndex::load(&path_str).ok()
    } else {
        None
    };
    let index = match index {
        Some(i) => i,
        None => match IdMapIndex::new(dim, bit_width) {
            Ok(i) => i,
            Err(_) => return std::ptr::null_mut(),
        },
    };
    Box::into_raw(Box::new(Handle {
        index,
        path: path_str,
        dim,
    })) as *mut c_void
}

#[no_mangle]
pub extern "C" fn od_tv_close(handle: *mut c_void) {
    if handle.is_null() {
        return;
    }
    unsafe {
        drop(Box::from_raw(handle as *mut Handle));
    }
}

#[no_mangle]
pub extern "C" fn od_tv_add(
    handle: *mut c_void,
    id: c_ulonglong,
    vector: *const c_float,
    dim: c_int,
) -> c_int {
    if handle.is_null() || vector.is_null() || dim <= 0 {
        return -1;
    }
    let handle = unsafe { &mut *(handle as *mut Handle) };
    let dim = dim as usize;
    let slice = unsafe { std::slice::from_raw_parts(vector, dim) };
    let ids = [id as u64];
    match handle.index.add_with_ids(slice, &ids) {
        Ok(()) => 0,
        Err(_) => -2,
    }
}

#[no_mangle]
pub extern "C" fn od_tv_remove(handle: *mut c_void, id: c_ulonglong) -> c_int {
    if handle.is_null() {
        return -1;
    }
    let handle = unsafe { &mut *(handle as *mut Handle) };
    if handle.index.remove(id as u64) {
        0
    } else {
        -2
    }
}

#[no_mangle]
pub extern "C" fn od_tv_search(
    handle: *mut c_void,
    query: *const c_float,
    dim: c_int,
    k: c_int,
    allowlist: *const c_ulonglong,
    allowlist_len: c_int,
    out_ids: *mut c_ulonglong,
    out_scores: *mut c_float,
    out_count: *mut c_int,
) -> c_int {
    if handle.is_null() || query.is_null() || out_ids.is_null() || out_scores.is_null() || out_count.is_null() {
        return -1;
    }
    if dim <= 0 || k <= 0 {
        return -1;
    }
    let handle = unsafe { &*(handle as *mut Handle) };
    let dim = dim as usize;
    let query_slice = unsafe { std::slice::from_raw_parts(query, dim) };
    let allow_ids: Vec<u64>;
    let allow = if allowlist.is_null() || allowlist_len <= 0 {
        None
    } else {
        let raw = unsafe { std::slice::from_raw_parts(allowlist, allowlist_len as usize) };
        allow_ids = raw.iter().map(|v| *v as u64).collect();
        Some(allow_ids.as_slice())
    };
    let result = handle
        .index
        .search_with_allowlist(query_slice, k as usize, allow);
    let (scores, ids) = match result {
        Ok(v) => v,
        Err(_) => return -2,
    };
    let count = ids.len().min(k as usize);
    unsafe {
        for i in 0..count {
            *out_ids.add(i) = ids[i] as c_ulonglong;
            *out_scores.add(i) = scores[i] as c_float;
        }
        *out_count = count as c_int;
    }
    0
}

#[no_mangle]
pub extern "C" fn od_tv_sync(handle: *mut c_void) -> c_int {
    if handle.is_null() {
        return -1;
    }
    let handle = unsafe { &mut *(handle as *mut Handle) };
    match handle.index.sync(&handle.path) {
        Ok(()) => 0,
        Err(_) => -2,
    }
}
