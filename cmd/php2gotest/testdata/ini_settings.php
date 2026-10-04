<?php
// INI settings are request state: a script writes one and reads it back, and
// the value survives the call. What php2go models is a closed list; anything
// else answers false rather than inventing a value.
var_dump(ini_get("arg_separator.output"));
var_dump(ini_get("zend.enable_gc"));
var_dump(ini_get("url_rewriter.tags"));
var_dump(ini_get("no.such.setting.at.all"));

var_dump(ini_set("arg_separator.output", ";"));
var_dump(ini_get("arg_separator.output"));
var_dump(ini_set("arg_separator.output", "&amp;"));
var_dump(ini_get("arg_separator.output"));

// Not every modelled setting is writable: the realpath cache is sized once.
var_dump(ini_set("realpath_cache_size", "1M"));
var_dump(ini_set("realpath_cache_ttl", "5"));

// syslog.filter takes one of four words and nothing else.
var_dump(ini_set("syslog.filter", "raw"), ini_get("syslog.filter"));
var_dump(ini_set("syslog.filter", "nonsense"), ini_get("syslog.filter"));

// A setting php2go does not model cannot be written either.
var_dump(ini_set("no.such.setting.at.all", "x"));
