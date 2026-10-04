// Package zend drives the real Zend compiler through the PHP embed SAPI.
//
// This is deliberately not a PHP extension. Go owns main, so the Go runtime is
// started normally rather than at dlopen time, and the php-fpm hazard of
// forking a process with a live Go runtime never arises.
//
// Build flags come from the environment; see hack/cgoenv.sh, which derives them
// from the Bazel output tree:
//
//	eval "$(hack/cgoenv.sh)" && go build ./...
package zend

/*
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sapi/embed/php_embed.h>
#include <Zend/zend_compile.h>
#include <Zend/zend_attributes.h>
#include <Zend/zend_stream.h>
#include <main/php_output.h>
#include <Zend/zend_exceptions.h>

// The auto globals the engine creates only when a script *names* one.
//
// With auto_globals_jit on, `$_SERVER`, `$_ENV` and `$_REQUEST` exist only if
// the compiler met one of them spelled directly -- and it creates the variable
// right then, at compile time, in the order it met them. `$GLOBALS["_SERVER"]`
// compiles to the very same FETCH_R as `$_SERVER` and does not arm it, so the
// op_array cannot answer the question; only the compile can. So each compile
// is bracketed: re-arm the three and take them out of the symbol table before,
// and read back after which ones the compile put there.
static const char *php2go_jit_globals[] = {"_SERVER", "_ENV", "_REQUEST"};

static void php2go_rearm_jit_globals(void) {
	for (int i = 0; i < 3; i++) {
		const char *name = php2go_jit_globals[i];
		size_t len = strlen(name);
		zend_auto_global *global = zend_hash_str_find_ptr(CG(auto_globals), name, len);
		if (global == NULL || !global->jit) {
			continue;
		}
		global->armed = 1;
		zend_hash_str_del(&EG(symbol_table), name, len);
	}
}

// php2go_armed_jit_globals writes the names the last compile created, in
// symbol-table order, each followed by a newline.
static void php2go_armed_jit_globals(char *out, size_t cap) {
	zend_string *key;
	size_t used = 0;
	out[0] = '\0';
	ZEND_HASH_FOREACH_STR_KEY(&EG(symbol_table), key) {
		if (key == NULL) {
			continue;
		}
		for (int i = 0; i < 3; i++) {
			const char *name = php2go_jit_globals[i];
			size_t len = strlen(name);
			if (zend_string_equals_cstr(key, name, len) && used + len + 2 <= cap) {
				memcpy(out + used, name, len);
				used += len;
				out[used++] = '\n';
				out[used] = '\0';
			}
		}
	} ZEND_HASH_FOREACH_END();
}

// Zend reports fatal conditions by longjmp ("bailout"). That must never unwind
// across a cgo boundary into Go, whose stacks it knows nothing about, so every
// entry point that can bail is wrapped in zend_try here.
// shebang says this is the file the request started from. Only that one has a
// `#!...` first line taken off, which is the SAPI's doing rather than the
// compiler's -- an included file with the same first line echoes it.
// eval's argument is compiled by a different entry point, and the difference is
// observable: a *file* ends in `RETURN 1`, so `include` of a file that returns
// nothing yields 1, while eval'd code ends in `RETURN null` and `eval("1;")`
// yields NULL. Compiling the string as a file would make every eval answer 1.
// thrown names the class of the exception the compile threw, if it threw
// one -- a CompileError the eval's caller can catch reads "Fatal error:" in
// the diagnostic exactly as a compile fatal that bails does.
static zend_op_array *php2go_compile_eval(const char *code, const char *name, int *bailed, zend_string **diagnostic, zend_string **thrown) {
	zend_op_array *op_array = NULL;
	zend_string *source;
	zval captured;

	*bailed = 0;
	*diagnostic = NULL;
	*thrown = NULL;
	php2go_rearm_jit_globals();
	source = zend_string_init(code, strlen(code), 0);
	php_output_start_default();
	zend_try {
		op_array = zend_compile_string(source, name, ZEND_COMPILE_POSITION_AFTER_OPEN_TAG);
		if (EG(exception) != NULL) {
			zend_object *error = EG(exception);
			*thrown = zend_string_copy(error->ce->name);
			EG(exception) = NULL;
			zend_exception_error(error, E_ERROR);
		}
	} zend_catch {
		*bailed = 1;
		op_array = NULL;
	} zend_end_try();
	if (php_output_get_contents(&captured) == SUCCESS) {
		*diagnostic = zval_get_string(&captured);
		zval_ptr_dtor(&captured);
	}
	php_output_discard();
	if (EG(exception) != NULL) {
		zend_clear_exception();
	}
	zend_string_release(source);
	return op_array;
}

static zend_op_array *php2go_compile_file(const char *filename, int shebang, int *bailed, zend_string **diagnostic) {
	zend_op_array *op_array = NULL;
	zend_file_handle handle;
	zval captured;

	*bailed = 0;
	*diagnostic = NULL;
	php2go_rearm_jit_globals();
	CG(skip_shebang) = shebang != 0;
	zend_stream_init_filename(&handle, filename);

	// **The script a request starts from is compiled first in a fresh
	// process**, so its runtime-definition keys -- and with them an
	// anonymous class's `$N` suffix -- count from 0. The harvest compiles many
	// scripts in one engine, which made the suffix depend on whatever was
	// compiled before (gh13097_b printed `$2` where php prints `$0`). Reset
	// for the primary script and restored after, never lowered: the keys
	// carry the file's own path, so a reused number cannot collide.
	uint32_t saved_rtd_key_counter = CG(rtd_key_counter);
	if (shebang) {
		CG(rtd_key_counter) = 0;
	}

	// Compilation can print: a compile-time fatal such as redeclaring a
	// function is reported as it happens. That text is the program's output as
	// far as a test is concerned, so it is captured rather than left to escape
	// to the process's own stdout.
	php_output_start_default();
	zend_try {
		op_array = zend_compile_file(&handle, ZEND_INCLUDE);
		// Most compile-time diagnostics are not printed at all: a syntax error
		// becomes a thrown ParseError and a bad modifier a thrown CompileError,
		// and the "Parse error: ... in FILE on line N" text only appears when
		// something reports them. zend_exception_error is what the CLI does, and
		// it is the only way to get that exact wording -- the message property
		// alone lacks the prefix, the file and the line. It also drains the
		// exception, which has to happen or it leaks into the next compile.
		if (EG(exception) != NULL) {
			zend_object *error = EG(exception);
			EG(exception) = NULL;
			zend_exception_error(error, E_ERROR);
		}
	} zend_catch {
		*bailed = 1;
		op_array = NULL;
	} zend_end_try();
	if (php_output_get_contents(&captured) == SUCCESS) {
		*diagnostic = zval_get_string(&captured);
		zval_ptr_dtor(&captured);
	}
	php_output_discard();

	if (EG(exception) != NULL) {
		zend_clear_exception();
	}
	if (CG(rtd_key_counter) < saved_rtd_key_counter) {
		CG(rtd_key_counter) = saved_rtd_key_counter;
	}

	zend_destroy_file_handle(&handle);
	return op_array;
}

static void php2go_free_op_array(zend_op_array *op_array) {
	if (op_array == NULL) {
		return;
	}
	destroy_op_array(op_array);
	efree(op_array);
}

// php2go_trial_link binds the script's top-level class declarations in order,
// as ZEND_DECLARE_CLASS would, and reports the first that ends in a fatal
// error: its opline and what the engine printed. A class is *linked* when it
// is declared -- parent, interfaces, traits, the compatibility of every
// override, an enum's forbidden methods -- and a failure there is a fatal the
// compiled program has to raise at that declaration. Stops, answering nothing
// more, at an exception (a parent only an autoloader could supply): what
// follows would depend on it. The caller forgets the file's classes after.
static zend_string *php2go_trial_link(zend_op_array *op_array, int *at) {
	*at = -1;
	for (uint32_t i = 0; i < op_array->last; i++) {
		zend_op *opline = &op_array->opcodes[i];
		if (opline->opcode != ZEND_DECLARE_CLASS) {
			continue;
		}
		// **A second declaration of a name this file already bound** --
		// the other branch of an `if`, or a top-level class bound at compile
		// time -- is the engine's `Cannot redeclare` only when both run,
		// which this link cannot know. It is skipped: the program raises
		// that fatal itself when it reaches the declaration with the name
		// taken, and runs only the declaration bound here otherwise
		// (gogen classVariants).
		zend_class_entry *bound = zend_hash_find_ptr(EG(class_table), Z_STR_P(RT_CONSTANT(opline, opline->op1)));
		if (bound != NULL && bound->type == ZEND_USER_CLASS && bound->info.user.filename != NULL &&
				op_array->filename != NULL && zend_string_equals(bound->info.user.filename, op_array->filename)) {
			continue;
		}
		int bailed = 0;
		zend_string *printed = NULL;
		zval captured;
		// **A frame executing the declaration**, as there would be at run
		// time, so each error names the location the engine gives it: the
		// DECLARE_CLASS line for most, `Unknown on line 0` for the
		// E_CORE_ERRORs that never name one (bug62609).
		zend_execute_data frame;
		memset(&frame, 0, sizeof(frame));
		frame.func = (zend_function *)op_array;
		frame.opline = opline;
		zend_execute_data *saved_frame = EG(current_execute_data);
		EG(current_execute_data) = &frame;
		php_output_start_default();
		zend_try {
			do_bind_class(RT_CONSTANT(opline, opline->op1),
				opline->op2_type == IS_CONST ? Z_STR_P(RT_CONSTANT(opline, opline->op2)) : NULL);
		} zend_catch {
			bailed = 1;
		} zend_end_try();
		EG(current_execute_data) = saved_frame;
		if (php_output_get_contents(&captured) == SUCCESS) {
			if (bailed) {
				printed = zval_get_string(&captured);
			}
			zval_ptr_dtor(&captured);
		}
		php_output_discard();
		if (bailed) {
			if (EG(exception) != NULL) {
				zend_clear_exception();
			}
			*at = (int)i;
			return printed;
		}
		if (EG(exception) != NULL) {
			zend_clear_exception();
			return NULL;
		}
	}
	return NULL;
}

// cgo cannot call C macros, so each one needs a shim.
static const char *php2go_zstr_val(zend_string *s) { return s ? ZSTR_VAL(s) : NULL; }
static size_t php2go_zstr_len(zend_string *s) { return s ? ZSTR_LEN(s) : 0; }

static uint8_t php2go_zval_type(const zval *zv) { return (uint8_t)Z_TYPE_P(zv); }
static zend_long php2go_zval_long(const zval *zv) { return Z_LVAL_P(zv); }
static int php2go_zval_extra_value(const zval *zv) { return Z_EXTRA_P(zv) == ZEND_EXTRA_VALUE; }
static double php2go_zval_double(const zval *zv) { return Z_DVAL_P(zv); }
static zend_string *php2go_zval_str(const zval *zv) { return Z_STR_P(zv); }
static const zval *php2go_literal(const zend_op_array *oa, int i) { return &oa->literals[i]; }

// An array literal whose elements are all constant is folded by the compiler
// into a single IS_ARRAY literal, so a consumer that cannot read one loses the
// whole value. Entries are copied out rather than iterated from Go, since a
// HashTable walk needs the engine's macros.
typedef struct {
	int is_int_key;
	zend_long int_key;
	zend_string *string_key;
	const zval *value;
} php2go_entry;

static int php2go_array_entries(const zval *zv, php2go_entry *out, int max) {
	int found = 0;
	zend_string *key;
	zend_ulong index;
	zval *value;

	if (Z_TYPE_P(zv) != IS_ARRAY) {
		return 0;
	}
	ZEND_HASH_FOREACH_KEY_VAL(Z_ARRVAL_P(zv), index, key, value) {
		if (found < max) {
			out[found].is_int_key = (key == NULL);
			out[found].int_key = (zend_long)index;
			out[found].string_key = key;
			out[found].value = value;
		}
		found++;
	} ZEND_HASH_FOREACH_END();
	return found;
}
static zend_string *php2go_var_name(const zend_op_array *oa, int i) { return oa->vars[i]; }

// arg_info[-1] holds the return type when the function declares one; the
// declared parameters start at index 0. Reading index -1 unconditionally would
// walk off the allocation.
static int php2go_has_return_type(const zend_op_array *oa) {
	return (oa->fn_flags & ZEND_ACC_HAS_RETURN_TYPE) != 0;
}

// num_args excludes the variadic parameter; when ZEND_ACC_VARIADIC is set there
// is one further arg_info entry at index num_args. Reflection does the same
// num_args++ when counting parameters.
static int php2go_is_variadic(const zend_op_array *oa) {
	return (oa->fn_flags & ZEND_ACC_VARIADIC) != 0;
}

static zend_string *php2go_arg_name(const zend_op_array *oa, int i) { return oa->arg_info[i].name; }
static int php2go_arg_by_ref(const zend_op_array *oa, int i) {
	return ZEND_ARG_SEND_MODE(&oa->arg_info[i]) != 0;
}
static int php2go_arg_variadic(const zend_op_array *oa, int i) {
	return ZEND_ARG_IS_VARIADIC(&oa->arg_info[i]) != 0;
}

// zend_type_to_string allocates; the caller releases it after copying.
static zend_string *php2go_arg_type(const zend_op_array *oa, int i) {
	zend_type type = oa->arg_info[i].type;
	if (!ZEND_TYPE_IS_SET(type)) {
		return NULL;
	}
	return zend_type_to_string(type);
}

static void php2go_release(zend_string *s) { if (s) zend_string_release(s); }

// Functions declared unconditionally at top level are bound into
// CG(function_table) during compilation rather than appearing in the file's own
// op_array, so they have to be collected separately. Filtering by filename
// avoids depending on the table being empty beforehand.
//
// Called twice: once with max == 0 to size the result, then to fill it.
static int php2go_collect_functions(const char *filename, zend_op_array **out, int max) {
	int found = 0;
	zend_function *fn;

	ZEND_HASH_FOREACH_PTR(CG(function_table), fn) {
		if (fn->type != ZEND_USER_FUNCTION || fn->op_array.filename == NULL) {
			continue;
		}
		if (strcmp(ZSTR_VAL(fn->op_array.filename), filename) != 0) {
			continue;
		}
		if (found < max) {
			out[found] = &fn->op_array;
		}
		found++;
	} ZEND_HASH_FOREACH_END();
	return found;
}

static zend_string *php2go_function_name(const zend_op_array *oa) { return oa->function_name; }

// Drops the class-entry cache from every interned class name that currently
// resolves to a user class. Must run *before* any user class is removed from
// the class table.
//
// Removing a class from the table is not enough on its own. An interned class
// name carries a map_ptr slot holding the last entry looked up under it, and
// nothing clears it when that entry is destroyed -- zend_lookup_class_ex
// answers from the slot before it ever consults the table, so the next file
// naming that class is handed freed memory. In a harvest that is a use-after-
// free the engine walks straight into: a child class whose parent was declared
// by an earlier file reaches zend_can_early_bind with a dangling parent_ce and
// segfaults.
//
// The slot is keyed on the string used at the *use* site, not on ce->name, so
// clearing ce->name alone misses any name written in a different case. Every
// class-name slot lives on an interned string, so CG(interned_strings) is the
// complete set to walk. Clearing a slot is always safe -- it is a cache, and a
// lookup that misses falls back to the table -- so classes that survive this
// round simply pay one lookup to be cached again.
static void php2go_drop_user_ce_caches(void) {
	zend_string *interned = NULL;

	ZEND_HASH_MAP_FOREACH_STR_KEY(&CG(interned_strings), interned) {
		zend_class_entry *cached;
		if (interned == NULL || !ZSTR_HAS_CE_CACHE(interned) || !ZSTR_VALID_CE_CACHE(interned)) {
			continue;
		}
		cached = ZSTR_GET_CE_CACHE(interned);
		if (cached != NULL && cached->type == ZEND_USER_CLASS) {
			ZSTR_SET_CE_CACHE(interned, NULL);
		}
	} ZEND_HASH_FOREACH_END();
}

static int php2go_ce_in_class_table(const zend_class_entry *needle) {
	zend_class_entry *live = NULL;

	ZEND_HASH_FOREACH_PTR(CG(class_table), live) {
		if (live == needle) {
			return 1;
		}
	} ZEND_HASH_FOREACH_END();
	return 0;
}

// Counts class-name cache slots holding a class entry the class table no longer
// knows about. Every one of those is a pointer to freed memory that
// zend_lookup_class_ex will hand to the compiler ahead of any table lookup, so
// the count is the harvester's memory-safety invariant and must be zero between
// files. Pointers are compared, never dereferenced, so this stays safe to call
// precisely when the invariant is broken.
static int php2go_dangling_ce_caches(void) {
	zend_string *interned = NULL;
	int dangling = 0;

	ZEND_HASH_MAP_FOREACH_STR_KEY(&CG(interned_strings), interned) {
		zend_class_entry *cached;
		if (interned == NULL || !ZSTR_HAS_CE_CACHE(interned) || !ZSTR_VALID_CE_CACHE(interned)) {
			continue;
		}
		cached = ZSTR_GET_CE_CACHE(interned);
		if (cached != NULL && !php2go_ce_in_class_table(cached)) {
			dangling++;
		}
	} ZEND_HASH_FOREACH_END();
	return dangling;
}

// Removes every user-defined function and class, whatever file declared it.
//
// Filename-scoped cleanup is not enough on its own: a compile that fails part
// way through has already bound some declarations, and the caller never learns
// which file the engine attributed them to. Left alone they accumulate across a
// harvest and eventually make later compiles behave differently -- or exhaust
// memory.
static int php2go_reset_user_symbols(void) {
	zend_string *key = NULL;
	zend_function *fn = NULL;
	zend_class_entry *ce = NULL;
	zend_string **keys = NULL;
	int count = 0, capacity = 0, removed = 0, i;

	ZEND_HASH_MAP_FOREACH_STR_KEY_PTR(CG(function_table), key, fn) {
		if (key == NULL || fn->type != ZEND_USER_FUNCTION) {
			continue;
		}
		if (count == capacity) {
			capacity = capacity ? capacity * 2 : 32;
			keys = erealloc(keys, capacity * sizeof(*keys));
		}
		keys[count++] = zend_string_copy(key);
	} ZEND_HASH_FOREACH_END();
	for (i = 0; i < count; i++) {
		if (zend_hash_del(CG(function_table), keys[i]) == SUCCESS) {
			removed++;
		}
		zend_string_release(keys[i]);
	}

	count = 0;
	php2go_drop_user_ce_caches();
	ZEND_HASH_MAP_FOREACH_STR_KEY_PTR(CG(class_table), key, ce) {
		if (key == NULL || ce->type != ZEND_USER_CLASS) {
			continue;
		}
		if (count == capacity) {
			capacity = capacity ? capacity * 2 : 32;
			keys = erealloc(keys, capacity * sizeof(*keys));
		}
		keys[count++] = zend_string_copy(key);
	} ZEND_HASH_FOREACH_END();
	for (i = 0; i < count; i++) {
		if (zend_hash_del(CG(class_table), keys[i]) == SUCCESS) {
			removed++;
		}
		zend_string_release(keys[i]);
	}

	// Constants defined by user code persist in the same table as the engine's
	// own, and a second script defining the same name would see a redefinition
	// warning that has nothing to do with it. User constants carry a distinct
	// module number, which is what separates them from an extension's.
	count = 0;
	{
		zend_constant *c = NULL;
		ZEND_HASH_MAP_FOREACH_STR_KEY_PTR(EG(zend_constants), key, c) {
			if (key == NULL || ZEND_CONSTANT_MODULE_NUMBER(c) != PHP_USER_CONSTANT) {
				continue;
			}
			if (count == capacity) {
				capacity = capacity ? capacity * 2 : 32;
				keys = erealloc(keys, capacity * sizeof(*keys));
			}
			keys[count++] = zend_string_copy(key);
		} ZEND_HASH_FOREACH_END();
	}
	for (i = 0; i < count; i++) {
		if (zend_hash_del(EG(zend_constants), keys[i]) == SUCCESS) {
			removed++;
		}
		zend_string_release(keys[i]);
	}

	if (keys != NULL) {
		efree(keys);
	}

	// Global variables outlive an eval as well. The interpreter always starts
	// from a fresh Ctx, so a leftover $arr here would have the oracle answer a
	// different question than the one being asked.
	zend_hash_clean(&EG(symbol_table));

	// So do the handlers set_error_handler and set_exception_handler installed.
	// A leftover error handler is the loudest kind of leak: one case installing
	// a handler that throws turns every later case's warning into a fatal.
	if (Z_TYPE(EG(user_error_handler)) != IS_UNDEF) {
		zval_ptr_dtor(&EG(user_error_handler));
		ZVAL_UNDEF(&EG(user_error_handler));
	}
	if (Z_TYPE(EG(user_exception_handler)) != IS_UNDEF) {
		zval_ptr_dtor(&EG(user_exception_handler));
		ZVAL_UNDEF(&EG(user_exception_handler));
	}
	zend_stack_clean(&EG(user_error_handlers), (void (*)(void *))zval_ptr_dtor, 1);
	zend_stack_clean(&EG(user_error_handlers_error_reporting), NULL, 1);
	zend_stack_clean(&EG(user_exception_handlers), (void (*)(void *))zval_ptr_dtor, 1);

	return removed;
}

// Compiling binds top-level functions into CG(function_table), where they
// persist. Harvesting a project would then fail on the second file declaring a
// given name -- which real projects do legitimately, through conditional
// includes, vendored duplicates, or alternative implementations. Files must
// therefore be independent, so what a file added is removed once collected.
//
// Keys are gathered before deleting; removing entries mid-iteration is fragile.
static int php2go_forget_functions(const char *filename) {
	zend_string *key = NULL;
	zend_function *fn = NULL;
	zend_string **keys = NULL;
	int count = 0, capacity = 0, removed = 0, i;

	ZEND_HASH_MAP_FOREACH_STR_KEY_PTR(CG(function_table), key, fn) {
		if (key == NULL || fn->type != ZEND_USER_FUNCTION || fn->op_array.filename == NULL) {
			continue;
		}
		if (strcmp(ZSTR_VAL(fn->op_array.filename), filename) != 0) {
			continue;
		}
		if (count == capacity) {
			capacity = capacity ? capacity * 2 : 16;
			keys = erealloc(keys, capacity * sizeof(*keys));
		}
		keys[count++] = zend_string_copy(key);
	} ZEND_HASH_FOREACH_END();

	for (i = 0; i < count; i++) {
		if (zend_hash_del(CG(function_table), keys[i]) == SUCCESS) {
			removed++;
		}
		zend_string_release(keys[i]);
	}
	if (keys != NULL) {
		efree(keys);
	}
	return removed;
}

// Closures and arrow functions are nested in the enclosing op_array rather than
// registered by name.
static uint32_t php2go_num_closures(const zend_op_array *oa) { return oa->num_dynamic_func_defs; }
static zend_op_array *php2go_closure(const zend_op_array *oa, uint32_t i) { return oa->dynamic_func_defs[i]; }

// A class entry records its source file under a user/internal union, so only
// user classes have one.
static zend_string *php2go_ce_filename(const zend_class_entry *ce) {
	return ce->type == ZEND_USER_CLASS ? ce->info.user.filename : NULL;
}

static int php2go_collect_classes(const char *filename, zend_class_entry **out, int max) {
	int found = 0;
	zend_class_entry *ce;

	ZEND_HASH_FOREACH_PTR(CG(class_table), ce) {
		zend_string *own = php2go_ce_filename(ce);
		if (own == NULL || strcmp(ZSTR_VAL(own), filename) != 0) {
			continue;
		}
		if (found < max) {
			out[found] = ce;
		}
		found++;
	} ZEND_HASH_FOREACH_END();
	return found;
}

// Internal classes -- Exception, ArrayObject, Iterator and several hundred more
// contributed by Core and the extensions -- are what user code inherits from.
// Their hierarchy is version-specific and large, so it is harvested rather than
// transcribed. Methods are C functions with no op_array, so only the shape is
// available here; behaviour has to be supplied on the Go side.
static int php2go_collect_internal_classes(zend_class_entry **out, int max) {
	int found = 0;
	zend_class_entry *ce;

	ZEND_HASH_FOREACH_PTR(CG(class_table), ce) {
		if (ce->type != ZEND_INTERNAL_CLASS) {
			continue;
		}
		if (found < max) {
			out[found] = ce;
		}
		found++;
	} ZEND_HASH_FOREACH_END();
	return found;
}

// Mirrors php2go_forget_functions: a class bound while compiling one file must
// not make the next file declaring that name fail.
static int php2go_forget_classes(const char *filename) {
	zend_string *key = NULL;
	zend_class_entry *ce = NULL;
	zend_string **keys = NULL;
	int count = 0, capacity = 0, removed = 0, i;

	php2go_drop_user_ce_caches();
	ZEND_HASH_MAP_FOREACH_STR_KEY_PTR(CG(class_table), key, ce) {
		zend_string *own = php2go_ce_filename(ce);
		if (key == NULL || own == NULL || strcmp(ZSTR_VAL(own), filename) != 0) {
			continue;
		}
		if (count == capacity) {
			capacity = capacity ? capacity * 2 : 16;
			keys = erealloc(keys, capacity * sizeof(*keys));
		}
		keys[count++] = zend_string_copy(key);
	} ZEND_HASH_FOREACH_END();

	for (i = 0; i < count; i++) {
		if (zend_hash_del(CG(class_table), keys[i]) == SUCCESS) {
			removed++;
		}
		zend_string_release(keys[i]);
	}
	if (keys != NULL) {
		efree(keys);
	}
	return removed;
}

static zend_string *php2go_ce_name(const zend_class_entry *ce) { return ce->name; }
static uint32_t php2go_ce_flags(const zend_class_entry *ce) { return ce->ce_flags; }
static uint32_t php2go_ce_enum_backing_type(const zend_class_entry *ce) { return ce->enum_backing_type; }

// parent and interfaces are unions resolved by ZEND_ACC_LINKED: before linking
// they are names, after linking they are class entries.
static zend_string *php2go_ce_parent(const zend_class_entry *ce) {
	if (ce->ce_flags & ZEND_ACC_LINKED) {
		return ce->parent ? ce->parent->name : NULL;
	}
	return ce->parent_name;
}

static uint32_t php2go_ce_num_interfaces(const zend_class_entry *ce) { return ce->num_interfaces; }

static zend_string *php2go_ce_interface(const zend_class_entry *ce, uint32_t i) {
	if (ce->ce_flags & ZEND_ACC_LINKED) {
		return ce->interfaces[i] ? ce->interfaces[i]->name : NULL;
	}
	return ce->interface_names[i].name;
}

static uint32_t php2go_ce_num_traits(const zend_class_entry *ce) { return ce->num_traits; }
static zend_string *php2go_ce_trait(const zend_class_entry *ce, uint32_t i) {
	return ce->trait_names[i].name;
}

// __get and __call are exactly the constructs that defeat static typing, so
// their presence is recorded rather than discovered later.
static int php2go_ce_has_get(const zend_class_entry *ce) { return ce->__get != NULL; }
static int php2go_ce_has_set(const zend_class_entry *ce) { return ce->__set != NULL; }
static int php2go_ce_has_call(const zend_class_entry *ce) { return ce->__call != NULL; }
static int php2go_ce_has_call_static(const zend_class_entry *ce) { return ce->__callstatic != NULL; }

static int php2go_collect_properties(const zend_class_entry *ce, zend_property_info **out, int max) {
	int found = 0;
	zend_property_info *info;

	ZEND_HASH_FOREACH_PTR((HashTable *)&ce->properties_info, info) {
		if (found < max) {
			out[found] = info;
		}
		found++;
	} ZEND_HASH_FOREACH_END();
	return found;
}

// Property order is observable: var_dump, foreach and get_object_vars all show
// declaration order. properties_info is keyed by name and its iteration order
// does not reproduce that, but the offset does -- it is the slot the engine
// assigned, handed out in declaration order with a parent's coming first.
static uint32_t php2go_prop_offset(const zend_property_info *info) { return info->offset; }

// A redeclaration diagnostic names the first declaration's line, which nothing
// else in the harvest carries.
static uint32_t php2go_ce_line_start(const zend_class_entry *ce) {
	return ce->type == ZEND_USER_CLASS ? ce->info.user.line_start : 0;
}
static uint32_t php2go_ce_line_end(const zend_class_entry *ce) {
	return ce->type == ZEND_USER_CLASS ? ce->info.user.line_end : 0;
}
static zend_string *php2go_ce_doc_comment(const zend_class_entry *ce) {
	return ce->type == ZEND_USER_CLASS ? ce->doc_comment : NULL;
}

// A hooked property's get and set bodies are ordinary op_arrays, but they live
// on the property rather than in the class's function table, so collecting the
// class's methods does not find them. A property with no backing storage is
// flagged virtual; the flag is read here rather than by comparing the offset
// against a sentinel transcribed into Go.
static zend_op_array *php2go_prop_hook(const zend_property_info *info, int kind) {
	if (info->hooks == NULL) {
		return NULL;
	}
	zend_function *hook = info->hooks[kind];
	if (hook == NULL || hook->type != ZEND_USER_FUNCTION) {
		return NULL;
	}
	return &hook->op_array;
}
static int php2go_prop_hook_get(void) { return ZEND_PROPERTY_HOOK_GET; }
static int php2go_prop_hook_set(void) { return ZEND_PROPERTY_HOOK_SET; }
// PHP 8.4 asymmetric visibility: a property may be readable more widely than it
// is writable. The write half is a separate pair of flags.
static const char *php2go_prop_set_visibility(const zend_property_info *info) {
	if (info->flags & ZEND_ACC_PRIVATE_SET) {
		return "private";
	}
	if (info->flags & ZEND_ACC_PROTECTED_SET) {
		return "protected";
	}
	return "";
}
static zend_string *php2go_prop_doc_comment(const zend_property_info *info) {
	return info->doc_comment;
}
static int php2go_prop_is_virtual(const zend_property_info *info) {
	return (info->flags & ZEND_ACC_VIRTUAL) != 0;
}

// Non-public property names are mangled in properties_info: protected becomes
// "\0*\0name" and private "\0ClassName\0name". Callers want the declared name,
// so unmangle rather than exposing the storage form.
static const char *php2go_prop_name(const zend_property_info *info, size_t *len) {
	const char *class_name = NULL;
	const char *prop_name = NULL;
	size_t prop_len = 0;

	if (zend_unmangle_property_name_ex(info->name, &class_name, &prop_name, &prop_len) == SUCCESS) {
		*len = prop_len;
		return prop_name;
	}
	*len = ZSTR_LEN(info->name);
	return ZSTR_VAL(info->name);
}
static uint32_t php2go_prop_flags(const zend_property_info *info) { return info->flags; }

// A class's property table includes the ones it inherited, so the entry alone
// does not say where a property was declared. That matters for statics: a
// subclass shares its parent's storage unless it redeclares the property.
static zend_string *php2go_prop_declaring_class(const zend_property_info *info) {
	return info->ce ? info->ce->name : NULL;
}
// A property's default is not stored on the property_info: it lives in the
// class's defaults table, indexed by the property's offset. Without it every
// declared property initializes to null instead of what the source said.
static const zval *php2go_prop_default(const zend_class_entry *ce, const zend_property_info *info) {
	int index;

	if (info->flags & ZEND_ACC_STATIC) {
		index = (int)info->offset;
		if (index >= 0 && index < ce->default_static_members_count) {
			return &ce->default_static_members_table[index];
		}
		return NULL;
	}
	index = (int)OBJ_PROP_TO_NUM(info->offset);
	if (index >= 0 && index < ce->default_properties_count) {
		return &ce->default_properties_table[index];
	}
	return NULL;
}

static zend_string *php2go_prop_type(const zend_property_info *info) {
	if (!ZEND_TYPE_IS_SET(info->type)) {
		return NULL;
	}
	return zend_type_to_string(info->type);
}

// Method modifiers are decided here rather than by masking fn_flags in Go: the
// ZEND_ACC_* bits are in scope on this side, and a shifted constant transcribed
// into another language is exactly the kind of thing that goes quietly wrong.
static int php2go_fn_is_static(const zend_op_array *oa) {
	return (oa->fn_flags & ZEND_ACC_STATIC) != 0;
}
static int php2go_fn_is_abstract(const zend_op_array *oa) {
	return (oa->fn_flags & ZEND_ACC_ABSTRACT) != 0;
}
static int php2go_fn_is_final(const zend_op_array *oa) {
	return (oa->fn_flags & ZEND_ACC_FINAL) != 0;
}
// PHP 8.4's #[\Deprecated]. The flag is on the function, but the message and
// the "since" string are attribute arguments, which may be positional or named
// and may themselves be unevaluated constant expressions.
static int php2go_attr_deprecated(HashTable *attributes) {
	return attributes != NULL &&
		zend_get_attribute_str(attributes, "deprecated", sizeof("deprecated") - 1) != NULL;
}
static const zval *php2go_attr_deprecated_arg(HashTable *attributes, const char *want) {
	zend_attribute *attr;
	uint32_t i, positional = 0;

	if (attributes == NULL) {
		return NULL;
	}
	attr = zend_get_attribute_str(attributes, "deprecated", sizeof("deprecated") - 1);
	if (attr == NULL) {
		return NULL;
	}
	for (i = 0; i < attr->argc; i++) {
		if (attr->args[i].name != NULL) {
			if (strcmp(ZSTR_VAL(attr->args[i].name), want) == 0) {
				return &attr->args[i].value;
			}
			continue;
		}
		// Positional: message first, then since.
		if ((positional == 0 && strcmp(want, "message") == 0) ||
		    (positional == 1 && strcmp(want, "since") == 0)) {
			return &attr->args[i].value;
		}
		positional++;
	}
	return NULL;
}
static HashTable *php2go_fn_attributes(const zend_op_array *oa) { return oa->attributes; }
static HashTable *php2go_ce_attributes(const zend_class_entry *ce) { return ce->attributes; }

// `use T { a as b; A::m insteadof B; }`. Both lists are NULL-terminated arrays
// hanging off the class entry, and they survive linking -- which matters,
// since a class using a known trait is linked before the harvest sees it.
static int php2go_trait_alias_count(const zend_class_entry *ce) {
	int n = 0;
	if (ce->trait_aliases == NULL) {
		return 0;
	}
	while (ce->trait_aliases[n] != NULL) {
		n++;
	}
	return n;
}
static zend_string *php2go_trait_alias_method(const zend_class_entry *ce, int i) {
	return ce->trait_aliases[i]->trait_method.method_name;
}
static zend_string *php2go_trait_alias_class(const zend_class_entry *ce, int i) {
	return ce->trait_aliases[i]->trait_method.class_name;
}
static zend_string *php2go_trait_alias_name(const zend_class_entry *ce, int i) {
	return ce->trait_aliases[i]->alias;
}
static uint32_t php2go_trait_alias_modifiers(const zend_class_entry *ce, int i) {
	return ce->trait_aliases[i]->modifiers;
}

static int php2go_trait_precedence_count(const zend_class_entry *ce) {
	int n = 0;
	if (ce->trait_precedences == NULL) {
		return 0;
	}
	while (ce->trait_precedences[n] != NULL) {
		n++;
	}
	return n;
}
static zend_string *php2go_trait_precedence_method(const zend_class_entry *ce, int i) {
	return ce->trait_precedences[i]->trait_method.method_name;
}
static zend_string *php2go_trait_precedence_class(const zend_class_entry *ce, int i) {
	return ce->trait_precedences[i]->trait_method.class_name;
}
static uint32_t php2go_trait_precedence_excludes(const zend_class_entry *ce, int i) {
	return ce->trait_precedences[i]->num_excludes;
}
static zend_string *php2go_trait_precedence_exclude(const zend_class_entry *ce, int i, uint32_t j) {
	return ce->trait_precedences[i]->exclude_class_names[j];
}
static HashTable *php2go_prop_attributes(const zend_property_info *p) { return p->attributes; }

// An attribute list is a packed HashTable of zend_attribute*. The struct is
// variable-length -- the arguments follow it inline -- so every field needs a
// shim rather than a Go mirror of the layout.
static uint32_t php2go_attr_count(HashTable *attrs) {
	return attrs == NULL ? 0 : zend_hash_num_elements(attrs);
}
static zend_attribute *php2go_attr_at(HashTable *attrs, uint32_t i) {
	zval *zv;
	if (attrs == NULL) {
		return NULL;
	}
	zv = zend_hash_index_find(attrs, i);
	return zv == NULL ? NULL : (zend_attribute *)Z_PTR_P(zv);
}
static zend_string *php2go_attr_full_name(const zend_attribute *a) { return a->name; }
// offset is 0 for an attribute on the declaration itself and the parameter's
// position plus one for an attribute on a parameter. Both kinds share the one
// list a function carries, so reading either means filtering by this.
static uint32_t php2go_attr_offset(const zend_attribute *a) { return a->offset; }
static uint32_t php2go_attr_argc(const zend_attribute *a) { return a->argc; }
static uint32_t php2go_attr_lineno(const zend_attribute *a) { return a->lineno; }
static zend_string *php2go_attr_arg_name(const zend_attribute *a, uint32_t i) {
	return i < a->argc ? a->args[i].name : NULL;
}
static const zval *php2go_attr_arg_value(const zend_attribute *a, uint32_t i) {
	return i < a->argc ? &a->args[i].value : NULL;
}
static HashTable *php2go_class_constant_attributes(const zend_class_constant *c) { return c->attributes; }
static zend_string *php2go_class_constant_doc_comment(const zend_class_constant *c) { return c->doc_comment; }
static zend_string *php2go_class_constant_type(const zend_class_constant *c) {
	if (!ZEND_TYPE_IS_SET(c->type)) {
		return NULL;
	}
	return zend_type_to_string(c->type);
}

static int php2go_fn_returns_ref(const zend_op_array *oa) {
	return (oa->fn_flags & ZEND_ACC_RETURN_REFERENCE) != 0;
}
static int php2go_fn_uses_this(const zend_op_array *oa) {
	return (oa->fn_flags & ZEND_ACC_USES_THIS) != 0;
}
static int php2go_fn_strict_types(const zend_op_array *oa) {
	return (oa->fn_flags & ZEND_ACC_STRICT_TYPES) != 0;
}
static const char *php2go_fn_visibility(const zend_op_array *oa) {
	if (oa->fn_flags & ZEND_ACC_PRIVATE) {
		return "private";
	}
	if (oa->fn_flags & ZEND_ACC_PROTECTED) {
		return "protected";
	}
	// A plain function has no visibility at all; only a method's is asked for.
	if (oa->fn_flags & ZEND_ACC_PUBLIC) {
		return "public";
	}
	return "";
}

// Internal methods have no op_array, so only their names are harvested. The
// engine's own table is the authority on which they are: a list transcribed
// from the manual goes stale one release later.
static int php2go_collect_engine_methods(const zend_class_entry *ce, zend_function **out, int max) {
	int found = 0;
	zend_function *fn;

	ZEND_HASH_FOREACH_PTR((HashTable *)&ce->function_table, fn) {
		if (fn->type == ZEND_USER_FUNCTION) {
			continue;
		}
		if (fn->common.scope != ce) {
			continue;
		}
		if (found < max) {
			out[found] = fn;
		}
		found++;
	} ZEND_HASH_FOREACH_END();
	return found;
}

// php2go_collect_engine_table is every internal method in a class's function
// table, inherited ones included, in table order: the order the engine walks
// a parent's methods in when it checks a child's overrides.
static int php2go_collect_engine_table(const zend_class_entry *ce, zend_function **out, int max) {
	int found = 0;
	zend_function *fn;

	ZEND_HASH_FOREACH_PTR((HashTable *)&ce->function_table, fn) {
		if (fn->type == ZEND_USER_FUNCTION) {
			continue;
		}
		if (found < max) {
			out[found] = fn;
		}
		found++;
	} ZEND_HASH_FOREACH_END();
	return found;
}

static zend_string *php2go_engine_method_name(const zend_function *fn) {
	return fn->common.function_name;
}
static zend_string *php2go_engine_method_scope(const zend_function *fn) {
	return fn->common.scope ? fn->common.scope->name : NULL;
}
static const char *php2go_engine_method_visibility(const zend_function *fn) {
	if (fn->common.fn_flags & ZEND_ACC_PRIVATE) {
		return "private";
	}
	if (fn->common.fn_flags & ZEND_ACC_PROTECTED) {
		return "protected";
	}
	return "public";
}
static int php2go_engine_method_is_static(const zend_function *fn) {
	return (fn->common.fn_flags & ZEND_ACC_STATIC) != 0;
}
static int php2go_engine_method_is_abstract(const zend_function *fn) {
	return (fn->common.fn_flags & ZEND_ACC_ABSTRACT) != 0;
}
// An engine method is a zend_internal_function, so its arg_info is read the
// same way an internal function's is -- names are plain C strings rather than
// zend_strings, and the return type sits at index -1.
static uint32_t php2go_engine_method_num_args(const zend_function *fn) {
	return fn->common.num_args;
}
static uint32_t php2go_engine_method_required_args(const zend_function *fn) {
	return fn->common.required_num_args;
}
static int php2go_engine_method_is_variadic(const zend_function *fn) {
	return (fn->common.fn_flags & ZEND_ACC_VARIADIC) != 0;
}
static int php2go_engine_method_has_return_type(const zend_function *fn) {
	return (fn->common.fn_flags & ZEND_ACC_HAS_RETURN_TYPE) != 0;
}
// A tentative return type is one the engine intends to enforce later. A
// mismatch against it is a deprecation rather than an error, and
// #[\ReturnTypeWillChange] silences even that.
static int php2go_engine_method_tentative_return(const zend_function *fn) {
	if (!(fn->common.fn_flags & ZEND_ACC_HAS_RETURN_TYPE)) {
		return 0;
	}
	return ZEND_ARG_TYPE_IS_TENTATIVE(&fn->internal_function.arg_info[-1]) != 0;
}
static const char *php2go_engine_method_arg_name(const zend_function *fn, int i) {
	return fn->internal_function.arg_info[i].name;
}
static int php2go_engine_method_arg_by_ref(const zend_function *fn, int i) {
	return ZEND_ARG_SEND_MODE(&fn->internal_function.arg_info[i]) != 0;
}
static int php2go_engine_method_is_final(const zend_function *fn) {
	return (fn->common.fn_flags & ZEND_ACC_FINAL) != 0;
}
static int php2go_engine_method_arg_variadic(const zend_function *fn, int i) {
	return ZEND_ARG_IS_VARIADIC(&fn->internal_function.arg_info[i]) != 0;
}
// An internal declaration keeps its parameter defaults as source text on the
// arg_info, where a user function has a RECV_INIT opline instead.
static const char *php2go_engine_method_arg_default(const zend_function *fn, int i) {
	return fn->internal_function.arg_info[i].default_value;
}
static zend_string *php2go_engine_method_arg_type(const zend_function *fn, int i) {
	zend_type type = fn->internal_function.arg_info[i].type;
	if (!ZEND_TYPE_IS_SET(type)) {
		return NULL;
	}
	return zend_type_to_string(type);
}

static int php2go_collect_methods(const zend_class_entry *ce, zend_op_array **out, int max) {
	int found = 0;
	zend_function *fn;

	ZEND_HASH_FOREACH_PTR((HashTable *)&ce->function_table, fn) {
		if (fn->type != ZEND_USER_FUNCTION) {
			continue;
		}
		// A class's function table also holds everything it inherited, so
		// collecting all of it would give a subclass its own copy of a parent's
		// method -- and then self:: inside that method would resolve to the
		// subclass. Only what this class actually declares belongs here; a
		// method reached through inheritance is found by walking the parent
		// chain. Trait methods do have the using class as their scope, and are
		// correctly kept.
		if (fn->common.scope != ce) {
			continue;
		}
		if (found < max) {
			out[found] = &fn->op_array;
		}
		found++;
	} ZEND_HASH_FOREACH_END();
	return found;
}

// Builtin signatures are read from the linked engine rather than parsed out of
// the *.stub.php sources. The stubs describe what PHP intends to build; the
// function table describes what this build actually has, which is what a
// translation must match. Compiling a stub is not even possible here -- it
// redeclares functions the engine already provides.
static int php2go_collect_builtins(zend_internal_function **out, int max) {
	int found = 0;
	zend_function *fn;

	ZEND_HASH_FOREACH_PTR(CG(function_table), fn) {
		if (fn->type != ZEND_INTERNAL_FUNCTION) {
			continue;
		}
		if (found < max) {
			out[found] = &fn->internal_function;
		}
		found++;
	} ZEND_HASH_FOREACH_END();
	return found;
}

// Engine constants live in one table that extensions add to at startup, so
// harvesting it after init captures every constant the build actually provides
// rather than a hand-kept subset.
static int php2go_collect_constants(zend_constant **out, zend_string **names, int max) {
	int found = 0;
	zend_string *key;
	zend_constant *c;

	ZEND_HASH_FOREACH_STR_KEY_PTR(EG(zend_constants), key, c) {
		if (found < max) {
			out[found] = c;
			names[found] = key;
		}
		found++;
	} ZEND_HASH_FOREACH_END();
	return found;
}

static const zval *php2go_constant_value(const zend_constant *c) { return &c->value; }

// INI directives are another engine registry whose contents depend on the
// linked modules and their build options. ini_get_all() sorts this table before
// exposing it, so the harvest does the same and preserves that observable
// order in the manifest.
static int php2go_collect_ini_entries(zend_ini_entry **out, int max) {
	int found = 0;
	zend_ini_entry *entry;

	zend_ini_sort_entries();
	ZEND_HASH_MAP_FOREACH_PTR(EG(ini_directives), entry) {
		if (found < max) {
			out[found] = entry;
		}
		found++;
	} ZEND_HASH_FOREACH_END();
	return found;
}

static zend_string *php2go_ini_name(const zend_ini_entry *entry) { return entry->name; }
static zend_string *php2go_ini_global_value(const zend_ini_entry *entry) {
	return entry->orig_value ? entry->orig_value : entry->value;
}
static zend_string *php2go_ini_local_value(const zend_ini_entry *entry) { return entry->value; }
static int php2go_ini_has_global_value(const zend_ini_entry *entry) {
	return entry->orig_value != NULL || entry->value != NULL;
}
static int php2go_ini_has_local_value(const zend_ini_entry *entry) { return entry->value != NULL; }
static uint32_t php2go_ini_access(const zend_ini_entry *entry) { return entry->modifiable; }

static const char *php2go_ini_extension(const zend_ini_entry *entry) {
	zend_module_entry *module;

	if (entry->module_number == 0) {
		return NULL;
	}
	ZEND_HASH_FOREACH_PTR(&module_registry, module) {
		if (module->module_number == entry->module_number) {
			return module->name;
		}
	} ZEND_HASH_FOREACH_END();
	return NULL;
}

static int php2go_module_count(void) { return zend_hash_num_elements(&module_registry); }
static const char *php2go_module_name_at(int wanted) {
	int index = 0;
	zend_module_entry *module;

	ZEND_HASH_FOREACH_PTR(&module_registry, module) {
		if (index++ == wanted) {
			return module->name;
		}
	} ZEND_HASH_FOREACH_END();
	return NULL;
}

// A constant the engine has deprecated -- E_STRICT since 8.4 -- reports when it
// is used. The flag is the build's own; nothing here decides which are.
static int php2go_constant_deprecated(const zend_constant *c) {
	return (ZEND_CONSTANT_FLAGS(c) & CONST_DEPRECATED) != 0;
}

// Class constants hang off the class entry in declaration order. An initializer
// the compiler could not fold stays an AST rather than a value, and is reported
// as absent instead of guessed at.
static int php2go_class_constant_count(const zend_class_entry *ce) {
	return zend_hash_num_elements(&ce->constants_table);
}

static int php2go_collect_class_constants(zend_class_entry *ce, zend_class_constant **out,
                                          zend_string **names, int max) {
	int found = 0;
	zend_string *key;
	zend_class_constant *c;

	ZEND_HASH_FOREACH_STR_KEY_PTR(&ce->constants_table, key, c) {
		if (found < max) {
			out[found] = c;
			names[found] = key;
		}
		found++;
	} ZEND_HASH_FOREACH_END();
	return found;
}

// A class with its own object-creation handler carries engine state a lazy
// object cannot stand in for. stdClass has none, which is why it may be made
// lazy while Exception and ArrayObject may not.
static int php2go_class_custom_create(const zend_class_entry *ce) {
	return ce->create_object != NULL;
}
// A class the engine refuses to clone has no clone handler at all -- Generator,
// Fiber and WeakReference. Everything else, including ArrayObject and Closure,
// clones through the standard one.
static int php2go_class_no_clone(const zend_class_entry *ce) {
	const zend_object_handlers *handlers = ce->default_object_handlers;
	return handlers == NULL || handlers->clone_obj == NULL;
}
static zend_string *php2go_class_constant_declaring_class(const zend_class_constant *c) {
	return c->ce ? c->ce->name : NULL;
}
static const zval *php2go_class_constant_value(const zend_class_constant *c) { return &c->value; }

static int php2go_class_constant_is_ast(const zend_class_constant *c) {
	return Z_TYPE(c->value) == IS_CONSTANT_AST;
}

// A constant initializer the compiler could not fold -- `self::B`, `A[0]`,
// `1 << 2` -- is kept as an AST and folded by the engine on first use. Reading
// the shape is the only way a runtime with no parser can do the same.
//
// These are read-only: walking an AST touches no engine state, unlike asking
// the engine to *evaluate* one, which corrupts memory across a corpus run.
static zend_ast *php2go_zval_ast(const zval *zv) { return Z_ASTVAL_P(zv); }
static uint32_t php2go_ast_kind(const zend_ast *ast) { return ast->kind; }
static uint32_t php2go_ast_attr(const zend_ast *ast) { return ast->attr; }
static uint32_t php2go_ast_lineno(zend_ast *ast) { return zend_ast_get_lineno(ast); }

static uint32_t php2go_ast_children(zend_ast *ast) {
	if (zend_ast_is_list(ast)) {
		return zend_ast_get_list(ast)->children;
	}
	if (zend_ast_is_special(ast)) {
		return 0;
	}
	return zend_ast_get_num_children(ast);
}

static zend_ast *php2go_ast_child(zend_ast *ast, uint32_t i) {
	if (zend_ast_is_list(ast)) {
		return zend_ast_get_list(ast)->child[i];
	}
	return ast->child[i];
}

// Both ZVAL and CONSTANT nodes keep their value in the same slot; the accessor
// in the header asserts on the kind, so the field is read directly.
static const zval *php2go_ast_zval(zend_ast *ast) {
	return &((zend_ast_zval *) ast)->val;
}

static uint32_t php2go_class_constant_flags(const zend_class_constant *c) {
	return ZEND_CLASS_CONST_FLAGS(c);
}

// An enum case is stored as a class constant whose value the compiler leaves as
// a ZEND_AST_CONST_ENUM_INIT node -- it cannot be a plain zval, because the case
// is an object of the enum's own class and that class does not exist yet.
//
// Evaluating it here would be wrong as well as awkward: PHP raises the
// backing-type mismatch when the case is first used, not when it is declared.
// Only the declared backing value is read out, and building the case object is
// left to the runtime.
static zend_ast *php2go_enum_case_backing_ast(const zend_class_constant *c) {
	zend_ast *ast;

	if (Z_TYPE(c->value) != IS_CONSTANT_AST) {
		return NULL;
	}
	ast = Z_ASTVAL(c->value);
	if (ast->kind != ZEND_AST_CONST_ENUM_INIT) {
		return NULL;
	}
	// child[2] is the backing value, absent for a pure enum. It is a plain
	// literal for `case A = 1;` and a whole expression for one naming a
	// constant, which the compiler cannot fold before the class is used.
	return ast->child[2];
}

// A function's static variables and a closure's `use` list share one table.
// ZEND_BIND_STATIC pulls an entry into a compiled variable at runtime; the
// declared initializer is only available here.
//
// An entry with no initializer -- a closure's `use ($n)`, which takes its value
// from the enclosing scope -- is stored as IS_UNDEF and reported as absent
// rather than as null, which is a value a program can observe.
static int php2go_static_var_count(const zend_op_array *oa) {
	return oa->static_variables ? zend_hash_num_elements(oa->static_variables) : 0;
}

static int php2go_collect_static_vars(const zend_op_array *oa, zend_string **names,
                                      zval **values, int max) {
	int found = 0;
	zend_string *key;
	zval *val;

	if (oa->static_variables == NULL) {
		return 0;
	}
	ZEND_HASH_FOREACH_STR_KEY_VAL(oa->static_variables, key, val) {
		if (found < max) {
			names[found] = key;
			values[found] = val;
		}
		found++;
	} ZEND_HASH_FOREACH_END();
	return found;
}

static int php2go_zval_is_undef(const zval *zv) { return Z_TYPE_P(zv) == IS_UNDEF; }

// PHP 8.4 compiles calls to some builtins into ZEND_FRAMELESS_ICALL_*, whose
// extended_value indexes a global table of functions rather than naming one.
// The table is built at startup from what the extensions register, so it is
// harvested for the same reason the opcode names are.
static int php2go_frameless_count(void) {
	int count = 0;
	while (zend_flf_functions != NULL && zend_flf_functions[count] != NULL) {
		count++;
	}
	return count;
}

static zend_string *php2go_frameless_name(int i) {
	return zend_flf_functions[i]->common.function_name;
}

static zend_string *php2go_builtin_name(const zend_internal_function *fn) { return fn->function_name; }
static uint32_t php2go_builtin_num_args(const zend_internal_function *fn) { return fn->num_args; }
static uint32_t php2go_builtin_required_args(const zend_internal_function *fn) { return fn->required_num_args; }
static int php2go_builtin_is_variadic(const zend_internal_function *fn) {
	return (fn->fn_flags & ZEND_ACC_VARIADIC) != 0;
}
static int php2go_builtin_has_return_type(const zend_internal_function *fn) {
	return (fn->fn_flags & ZEND_ACC_HAS_RETURN_TYPE) != 0;
}

// Internal arg_info names are plain C strings, unlike a user function's
// zend_string.
static const char *php2go_builtin_arg_name(const zend_internal_function *fn, int i) {
	return fn->arg_info[i].name;
}
static int php2go_builtin_arg_by_ref(const zend_internal_function *fn, int i) {
	return ZEND_ARG_SEND_MODE(&fn->arg_info[i]) != 0;
}
static int php2go_builtin_arg_variadic(const zend_internal_function *fn, int i) {
	return ZEND_ARG_IS_VARIADIC(&fn->arg_info[i]) != 0;
}
// An internal parameter's default is source text on the arg_info. It is NULL
// for a required parameter, and also for an optional one whose default the
// declaration could not write down -- which the engine refuses to invent.
static const char *php2go_builtin_arg_default(const zend_internal_function *fn, int i) {
	return ((zend_internal_arg_info *)fn->arg_info)[i].default_value;
}
static zend_string *php2go_builtin_arg_type(const zend_internal_function *fn, int i) {
	zend_type type = fn->arg_info[i].type;
	if (!ZEND_TYPE_IS_SET(type)) {
		return NULL;
	}
	return zend_type_to_string(type);
}

// The opcode table likewise comes from the engine: zend_get_opcode_name is
// authoritative for the build, where a parsed header could drift from it.
static const char *php2go_opcode_name(uint8_t opcode) { return zend_get_opcode_name(opcode); }

static const char *php2go_php_version(void) { return PHP_VERSION; }

// The SPL autoload registry is a file-static in ext/spl, so it cannot be
// cleared by reaching into it. PHP itself can, and asking PHP is the only lever
// available -- without it an autoloader registered by one evaluation is still
// consulted by the next, which makes the oracle answer a question that was
// never asked.
static void php2go_clear_autoloaders(void) {
	zend_try {
		zend_eval_string(
			"if (function_exists('spl_autoload_functions')) {"
			"  foreach (spl_autoload_functions() as $f) { spl_autoload_unregister($f); }"
			"}", NULL, "php2go-reset");
	} zend_catch {
	} zend_end_try();
}

// error_reporting is an ini setting, not a constant, so it cannot come from the
// constant table. Which levels start enabled is a property of the build.
// Whether assert() is compiled in and enabled. ZEND_ASSERT_CHECK branches on
// it, so the IR is only meaningful next to a build with the same setting.
static zend_long php2go_assertions(void) { return EG(assertions); }

static zend_long php2go_error_reporting(void) { return (zend_long)EG(error_reporting); }
static void php2go_set_error_reporting(zend_long mask) { EG(error_reporting) = (int)mask; }
// php_request_startup deliberately leaves this set for php_execute_script to
// clear. The oracle executes through zend_eval_string instead, so it must make
// the same transition explicitly or php_error_docref labels runtime warnings
// as "PHP Request Startup".
static void php2go_begin_execution(void) { PG(during_request_startup) = 0; }

// Evaluates PHP and returns whatever it printed. This is the differential
// oracle: the same source can be run by the engine and by the interpreter and
// the two outputs compared, which is the only way to be sure the runtime
// matches rather than merely looks right.
//
// Output is captured through PHP's own buffering rather than by redirecting a
// file descriptor, so it works in-process and cannot interleave with Go's.
// The name is what diagnostics report as the file. It is a parameter because
// the third-arm harness compares an evaluated string against a *compiled
// program*, and a compiled program names its own source: with the name fixed,
// every diagnostic differed in the filename and no such test could ever pass.
static zend_string *php2go_eval(const char *code, const char *name, int *bailed) {
	zval captured;
	zend_string *result = NULL;

	*bailed = 0;
	php_output_start_default();
	zend_try {
		zend_eval_string((char *)code, NULL, (char *)name);
		// An exception that escapes the eval is still merely pending: the
		// "Fatal error: Uncaught ..." text is produced at request shutdown,
		// which does not happen here. Reporting it now is what makes the
		// oracle able to answer questions about that message, and a large
		// share of the .phpt corpus expects exactly it.
		if (EG(exception) != NULL) {
			zend_object *ex = EG(exception);
			EG(exception) = NULL;
			zend_exception_error(ex, E_ERROR);
		}
		// Destructors are part of what a script prints, and they run at
		// request shutdown, which this eval does not reach. `shutdown_destructors`
		// is these two steps: the symbol table is torn down in reverse
		// insertion order, then whatever objects are left are destroyed. Doing
		// it here keeps that output inside the capture -- and it also has to
		// happen before the class table is reset, since destroying an object
		// whose class entry has already been freed is a crash.
		zend_hash_graceful_reverse_destroy(&EG(symbol_table));
		zend_hash_init(&EG(symbol_table), 8, NULL, ZVAL_PTR_DTOR, 0);
		zend_objects_store_call_destructors(&EG(objects_store));
		if (EG(exception) != NULL) {
			zend_object *ex = EG(exception);
			EG(exception) = NULL;
			zend_exception_error(ex, E_ERROR);
		}
	} zend_catch {
		*bailed = 1;
	} zend_end_try();

	if (php_output_get_contents(&captured) == SUCCESS) {
		result = zval_get_string(&captured);
		zval_ptr_dtor(&captured);
	}
	php_output_discard();
	return result;
}

// **Every string eval compiles, recorded** (php2go_record_evals): the
// name the engine gives it and its source, in the order compiled, appended
// to a file as a 32-bit length and the bytes of each. The hook sits in front
// of zend_compile_string, which is where ZEND_INCLUDE_OR_EVAL's eval goes;
// what else reaches it -- the request's own script under php2go_eval -- is
// the reader's to tell apart by name. Flushed per record, so a request that
// ends in a fatal keeps what it compiled before.
static zend_op_array *(*php2go_prev_compile_string)(zend_string *, const char *, zend_compile_position) = NULL;
static FILE *php2go_eval_log = NULL;

static void php2go_log_bytes(const char *bytes, size_t len) {
	uint32_t n = (uint32_t)len;
	fwrite(&n, sizeof n, 1, php2go_eval_log);
	fwrite(bytes, 1, len, php2go_eval_log);
}

static zend_op_array *php2go_recording_compile_string(zend_string *source, const char *filename, zend_compile_position position) {
	if (php2go_eval_log != NULL) {
		php2go_log_bytes(filename, strlen(filename));
		php2go_log_bytes(ZSTR_VAL(source), ZSTR_LEN(source));
		fflush(php2go_eval_log);
	}
	return php2go_prev_compile_string(source, filename, position);
}

static int php2go_record_evals(const char *path) {
	FILE *log = fopen(path, "wb");
	if (log == NULL) {
		return -1;
	}
	if (php2go_eval_log != NULL) {
		fclose(php2go_eval_log);
	}
	php2go_eval_log = log;
	if (php2go_prev_compile_string == NULL) {
		php2go_prev_compile_string = zend_compile_string;
		zend_compile_string = php2go_recording_compile_string;
	}
	return 0;
}

static void php2go_stop_record_evals(void) {
	if (php2go_prev_compile_string != NULL) {
		if (zend_compile_string == php2go_recording_compile_string) {
			zend_compile_string = php2go_prev_compile_string;
		}
		php2go_prev_compile_string = NULL;
	}
	if (php2go_eval_log != NULL) {
		fclose(php2go_eval_log);
		php2go_eval_log = NULL;
	}
}

// znode_op is a union; cgo renders it as opaque bytes. Reading it through C
// keeps the interpretation in one place instead of duplicating the layout.
static uint32_t php2go_op1(const zend_op *op) { return op->op1.num; }
static uint32_t php2go_op2(const zend_op *op) { return op->op2.num; }
static uint32_t php2go_result(const zend_op *op) { return op->result.num; }

// Raw operands are addresses, not indices, and neither survives serialization:
//
//   IS_CONST is a byte offset from the *opline itself* (RT_CONSTANT), so it
//   differs for every instruction referring to the same literal.
//   IS_CV, IS_TMP_VAR and IS_VAR are byte offsets into the call frame, past a
//   header whose size is a build detail (EX_VAR_TO_NUM).
//
// Both are normalized here to plain indices, which is what a consumer can
// actually use.
static int32_t php2go_op1_const_index(const zend_op_array *oa, const zend_op *op) {
	return (int32_t)(RT_CONSTANT(op, op->op1) - oa->literals);
}

static int32_t php2go_op2_const_index(const zend_op_array *oa, const zend_op *op) {
	return (int32_t)(RT_CONSTANT(op, op->op2) - oa->literals);
}

static int32_t php2go_probe_raw_op2(const zend_op *op) { return (int32_t)op->op2.constant; }
static int php2go_probe_last_literal(const zend_op_array *oa) { return oa->last_literal; }
static const char *php2go_probe_lit_at(const zend_op_array *oa, int i) {
	const zval *zv = &oa->literals[i];
	return Z_TYPE_P(zv) == IS_STRING ? Z_STRVAL_P(zv) : "<non-string>";
}
static uint32_t php2go_var_slot(uint32_t offset) { return EX_VAR_TO_NUM(offset); }

// A live range's var is not an operand: the low bits carry what kind of
// temporary it is, and the rest is the same byte offset an operand holds.
static uint32_t php2go_live_slot(uint32_t v) { return EX_VAR_TO_NUM(v & ~ZEND_LIVE_MASK); }
static uint32_t php2go_live_kind(uint32_t v) { return v & ZEND_LIVE_MASK; }
static uint32_t php2go_live_loop_kind(void) { return ZEND_LIVE_LOOP; }

// Diagnostics for operand decoding.
static int32_t php2go_raw_op2(const zend_op *op) { return (int32_t)op->op2.constant; }
static long php2go_literals_offset(const zend_op_array *oa) {
	return (long)((const char *)oa->literals - (const char *)oa->opcodes);
}
static long php2go_opline_offset(const zend_op_array *oa, int index) {
	return (long)((const char *)&oa->opcodes[index] - (const char *)oa->opcodes);
}

// Jump targets are byte offsets from the opline as well (OP_JMP_ADDR), so they
// need the same treatment. Which operands are jumps is per-opcode knowledge the
// engine already holds, so it is asked rather than tabulated here.
// The operand-kind values (ZEND_VM_OP_NUM, _JMP_ADDR, _TRY_CATCH, _CACHE_SLOT
// and friends) are an enumerated field under ZEND_VM_OP_MASK, not independent
// bits. Testing with a bitwise AND matches every value that happens to share a
// bit -- CACHE_SLOT (0xa0), TRY_CATCH (0x30) and CLASS_FETCH (0x70) all look
// like JMP_ADDR (0x20) that way, and their operands then get mangled into
// nonsense instruction indices. The field must be masked and compared.
static int php2go_op1_is_jump(uint8_t opcode) {
	uint32_t flags = ZEND_VM_OP1_FLAGS(zend_get_opcode_flags(opcode));
	return (flags & ZEND_VM_OP_MASK) == ZEND_VM_OP_JMP_ADDR;
}

static int php2go_op2_is_jump(uint8_t opcode) {
	uint32_t flags = ZEND_VM_OP2_FLAGS(zend_get_opcode_flags(opcode));
	return (flags & ZEND_VM_OP_MASK) == ZEND_VM_OP_JMP_ADDR;
}

// extended_value carries a jump target for some opcodes too -- FE_FETCH_R puts
// the loop-exit branch there, since op2 is taken by the key destination. The
// flag is another enumerated field, so it needs masking rather than a bit test.
static int php2go_ext_is_jump(uint8_t opcode) {
	uint32_t flags = zend_get_opcode_flags(opcode);
	return (flags & ZEND_VM_EXT_MASK) == ZEND_VM_EXT_JMP_ADDR;
}

// The width of znode_op depends on build flags, so the layout must be reported
// by the build that actually compiled PHP rather than assumed.
static int php2go_abs_jmp_addr(void) {
#if ZEND_USE_ABS_JMP_ADDR
	return 1;
#else
	return 0;
#endif
}

static int php2go_abs_const_addr(void) {
#if ZEND_USE_ABS_CONST_ADDR
	return 1;
#else
	return 0;
#endif
}

static size_t php2go_sizeof_znode_op(void) { return sizeof(znode_op); }
static size_t php2go_sizeof_zend_op(void) { return sizeof(zend_op); }
*/
import "C"

import (
	"encoding/binary"
	"errors"
	"fmt"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"unsafe"
)

// Layout reports how the linked PHP was built. znode_op's width changes with
// these flags, so anything decoding operands must consult them rather than
// assume a size.
type Layout struct {
	AbsoluteJumpAddresses     bool
	AbsoluteConstantAddresses bool
	SizeofZnodeOp             int
	SizeofZendOp              int
}

// DetectLayout reads the layout compiled into the linked library.
func DetectLayout() Layout {
	return Layout{
		AbsoluteJumpAddresses:     C.php2go_abs_jmp_addr() != 0,
		AbsoluteConstantAddresses: C.php2go_abs_const_addr() != 0,
		SizeofZnodeOp:             int(C.php2go_sizeof_znode_op()),
		SizeofZendOp:              int(C.php2go_sizeof_zend_op()),
	}
}

// CompileError reports that a source failed to compile, carrying whatever the
// engine printed or threw. Many PHP tests are written to produce exactly this,
// so the message is data rather than noise.
type CompileError struct {
	Path       string
	Diagnostic string
	// Bailed distinguishes an engine bailout from an ordinary compile error.
	Bailed bool
	// Thrown is the class of the exception the compile threw -- ParseError,
	// CompileError -- which the code that asked for it can catch; empty
	// for a fatal that is not an exception. Set by CompileEvalString.
	Thrown string
}

func (e *CompileError) Error() string {
	if e.Diagnostic != "" {
		return fmt.Sprintf("zend: %s: %s", e.Path, strings.TrimSpace(e.Diagnostic))
	}
	if e.Bailed {
		return fmt.Sprintf("zend: compilation of %s bailed out", e.Path)
	}
	return fmt.Sprintf("zend: could not compile %s", e.Path)
}

// Opline is one instruction of a compiled op_array, with the source line the
// whole pipeline is annotated by.
type Opline struct {
	Index      int
	Opcode     uint8
	OpcodeName string
	Op1Type    uint8
	Op2Type    uint8
	ResultType uint8
	// SmartBranch is "jmpz", "jmpnz", or empty: set when the engine fused this
	// comparison with the branch that follows it.
	SmartBranch string
	Op1         uint32
	Op2         uint32
	Result      uint32
	Extended    uint32
	Line        uint32
}

// Literal is a compile-time constant referenced by an IS_CONST operand.
type Literal struct {
	Kind  string
	Int   int64
	Float float64
	Str   string
	// Entries holds the elements when Kind is "array". An array literal whose
	// elements are all constant is folded into a single literal by the
	// compiler, so this is the only place its contents appear.
	Entries []LiteralEntry
	// Expr holds the shape when Kind is "expr": an initializer the compiler
	// could not fold, such as `self::B` or `A[0]`. The engine keeps it as an
	// AST and evaluates it on first use.
	Expr *Expr
	// ExtraValue marks a literal whose original spelling the compiler kept
	// in the next literal -- a numeric-string offset folded to an int, which
	// an ArrayAccess object still receives as the string.
	ExtraValue bool
}

// Expr is one node of an unevaluated constant initializer.
type Expr struct {
	// Kind names what the node does: "literal", "constant", "class_constant",
	// "dim", "array", "binary", "unary", "conditional", "coalesce",
	// "class_name". Anything else the harvest could not read is "unsupported",
	// which keeps the constant a hole rather than inventing a value.
	Kind string
	// Value is set for "literal".
	Value Literal
	// Name is the constant or class name; Member the class constant's name.
	Name   string
	Member string
	// Op is the engine's opcode number for "binary" and "unary", resolved
	// through the manifest's opcode table rather than transcribed.
	Op uint32
	// Line is the source line the node was written on, which is where the
	// engine attributes a diagnostic raised while evaluating it.
	Line     uint32
	Children []*Expr
	// HasKey marks an array element written with an explicit key.
	HasKey bool
}

// LiteralEntry is one element of a constant array literal.
type LiteralEntry struct {
	IntKey    bool
	IntKeyVal int64
	StringKey string
	Value     Literal
}

// ArgInfo is a declared parameter. PHP 8 records real types here, which is
// high-quality type information available without any inference.
type ArgInfo struct {
	Name     string
	Type     string
	ByRef    bool
	Variadic bool
	// Default is the parameter's default as the declaration wrote it, which an
	// internal declaration keeps here. A user function keeps it in a RECV_INIT
	// opline instead, and leaves this empty.
	Default string
	// Attributes are those written on this parameter, which the engine keeps
	// in the function's list under an offset of one past the position.
	Attributes []Attribute
}

// TryCatchRange maps a try block to its catch and finally handlers.
type TryCatchRange struct {
	Try        uint32
	Catch      uint32
	Finally    uint32
	FinallyEnd uint32
}

// LiveRange records where a temporary stays live, which the engine needs to
// clean up after an abrupt exit.
type LiveRange struct {
	Var   uint32
	Start uint32
	End   uint32
	// Kind is what the temporary is, which decides how it is released:
	// ZEND_LIVE_LOOP is a foreach's iterator, ZEND_LIVE_NEW a half-built
	// object, ZEND_LIVE_SILENCE an `@` and ZEND_LIVE_ROPE a string being
	// concatenated.
	Kind uint32
}

// LiveLoop is the live-range kind of a foreach's iterator temporary.
var LiveLoop = uint32(C.php2go_live_loop_kind())

// OpArray is a compiled unit.
type OpArray struct {
	// Diagnostic is what the compiler printed while producing this op_array.
	// Only a file's own op_array carries one.
	Diagnostic string
	// AutoGlobals are the just-in-time superglobals -- `_SERVER`, `_ENV`,
	// `_REQUEST` -- that compiling this file created, in the order the engine
	// created them. Only a file's own op_array carries them.
	AutoGlobals []string

	Filename  string
	LineStart uint32
	LineEnd   uint32
	// DocComment is the `/** ... */` written before the declaration.
	DocComment string
	NumVars    int
	NumTemps   uint32
	Oplines    []Opline

	// VarNames are the compiled variables, indexed as op operands / sizeof(zval).
	VarNames     []string
	Literals     []Literal
	Args         []ArgInfo
	ReturnType   string
	NumArgs      uint32
	RequiredArgs uint32
	TryCatch     []TryCatchRange
	LiveRanges   []LiveRange

	// StaticVars are the entries ZEND_BIND_STATIC pulls into a compiled
	// variable: a function's `static $x = ...` and a closure's `use` list, which
	// the engine keeps in one table.
	StaticVars []StaticVar

	// Name is empty for a file's own op_array and set for functions.
	Name string

	// Modifiers, which are only meaningful for a method. Visibility is empty
	// for anything that is not one. Whether a method is static decides both
	// whether $this is bound and how a stack trace names the call, and neither
	// can be inferred from the body.
	Visibility string
	Static     bool
	UsesThis   bool
	Attributes []Attribute
	// ReturnsRef marks `function &f()` -- and a `&get` property hook, which is
	// the same flag and decides whether a write can reach through it.
	ReturnsRef bool
	// Deprecated records #[\Deprecated] and its two optional arguments, which
	// build the suffix of the diagnostic a call to it produces.
	Deprecated        bool
	DeprecatedMessage *Literal
	DeprecatedSince   *Literal
	Abstract          bool
	Final             bool

	// StrictTypes reports whether the file this was compiled from declared
	// strict_types=1. Type coercion is decided by the *caller's* file, so the
	// flag has to travel with the op_array rather than with the unit.
	StrictTypes bool
	// Closures are nested op_arrays: closures and arrow functions, which are
	// not registered by name.
	Closures []*OpArray
}

// EngineMethod is a method the linked build provides rather than the program.
type EngineMethod struct {
	Name       string
	Visibility string
	Static     bool
	Abstract   bool
	// Args and ReturnType are the declared signature, which a class
	// implementing an engine interface has to be compatible with.
	Args         []ArgInfo
	ReturnType   string
	RequiredArgs uint32
	Variadic     bool
	// TentativeReturn marks a return type the engine has not started enforcing.
	TentativeReturn bool
	// Scope is the class that declares the method, which a diagnostic
	// comparing an override with it names.
	Scope string
	// Final is what reflection reports and what a subclass may not override.
	Final bool
}

// StaticVar is one entry of an op_array's static-variable table.
type StaticVar struct {
	Name string
	// HasDefault is false for a closure's `use` variable, whose value comes
	// from the enclosing scope rather than an initializer.
	Default    Literal
	HasDefault bool
	// SetVisibility is the write half of an asymmetrically visible property --
	// `public private(set) $p`. Empty when writing is as visible as reading.
	SetVisibility string
	// Virtual marks a hooked property with no backing storage: it has no slot
	// on the object and exists only through its hooks.
	Virtual bool
	// GetHook and SetHook are a PHP 8.4 hooked property's bodies. They live on
	// the property rather than in the class's function table.
	GetHook *OpArray
	SetHook *OpArray
}

// Property is a declared class property. PHP 8 typed properties give real
// types here without inference.
type Property struct {
	// Final is ZEND_ACC_FINAL on the property.
	Final bool
	// DocComment is the property's `/** ... */`.
	DocComment string
	Name       string
	// DeclaringClass is where the property was written, which is not always the
	// class it was read from -- inherited properties appear in the child's
	// table too. Static properties share storage with their declaration.
	DeclaringClass string
	Type           string
	Visibility     string
	Static         bool
	Readonly       bool
	// Default is the declared initializer. HasDefault distinguishes "= null"
	// from no initializer at all, which for a typed property is the difference
	// between null and uninitialized.
	Default    Literal
	HasDefault bool
	// SetVisibility is the write half of an asymmetrically visible property --
	// `public private(set) $p`. Empty when writing is as visible as reading.
	SetVisibility string
	// Attributes are every `#[...]` written on the property, in source order.
	Attributes []Attribute
	// Virtual marks a hooked property with no backing storage: it has no slot
	// on the object and exists only through its hooks.
	Virtual bool
	// Order is where the engine's properties_info holds this property, which
	// is declaration order within a class. The list travels sorted by slot
	// offset instead -- what var_dump and foreach expose -- and the two differ
	// for a static property: it has no slot among the instance ones, and
	// reflection still lists it where it was written.
	Order uint32
	// GetHook and SetHook are a PHP 8.4 hooked property's bodies. They live on
	// the property rather than in the class's function table.
	GetHook *OpArray
	SetHook *OpArray
}

// Class is a user-declared class, interface, trait, or enum.
type Class struct {
	Name string
	// Kind is "class", "interface", "trait", or "enum".
	Kind       string
	Parent     string
	Interfaces []string
	Traits     []string
	Abstract   bool
	Final      bool
	// EnumBackingType is "int", "string", or "" for a pure enum.
	EnumBackingType string
	// LinkError is what the engine said when it refused to bind this class:
	// an incompatible declaration, a trait conflict, an unimplemented abstract
	// method. The class is declared at run time, so the message belongs there.
	LinkError  string
	Properties []Property
	Methods    []*OpArray
	// EngineMethods are the methods the build supplies for this class. They
	// have no op_array, so only what the reflective builtins need travels.
	EngineMethods []EngineMethod
	// EngineTable is every internal method the class has, inherited ones
	// included, in function-table order: what a subclass's overrides are
	// checked against, in the order the engine checks them.
	EngineTable []EngineMethod

	// Magic accessors defeat static typing: a property read may run arbitrary
	// code, so a class carrying these cannot be compiled to plain struct field
	// access. Recorded here so the analysis never has to rediscover it.
	HasGet  bool
	HasSet  bool
	HasCall bool
	// AllowDynamicProperties reports the #[AllowDynamicProperties] opt-in,
	// without which assigning an undeclared property is deprecated.
	AllowDynamicProperties bool
	// Readonly reports PHP 8.2's `readonly class`. Every property the class
	// declares is readonly, but one it gets from a trait is not -- which is
	// why the flag is needed rather than inferring it from the property list.
	Readonly bool
	// NoDynamicProperties reports the engine's refusal to grow a class at all,
	// which a few of its own classes carry.
	NoDynamicProperties bool
	// NoClone reports a class the engine refuses to clone at all: it has no
	// clone handler, so `clone $o` is an Error rather than a copy.
	NoClone bool
	// CustomCreate reports a class with its own object-creation handler, which
	// carries engine state a lazy object cannot stand in for. It is inherited,
	// so a user class extending one has it too.
	CustomCreate bool
	// NotSerializable reports a class the engine refuses to serialize or
	// unserialize.
	NotSerializable bool
	HasCallStatic   bool
	Attributes      []Attribute
	// TraitAliases and TraitPrecedences are the `as` and `insteadof` clauses of
	// a `use` statement. Without them a trait's methods are reachable only
	// under their own names.
	TraitAliases     []TraitAlias
	TraitPrecedences []TraitPrecedence
	// Filename, LineStart and LineEnd locate the declaration, which a redeclaration
	// diagnostic has to name.
	Filename  string
	LineStart uint32
	LineEnd   uint32
	// DocComment is the class's `/** ... */`.
	DocComment string

	Constants []ClassConstant
}

// ClassConstant is a `const` declared in a class body, or a case of an enum --
// the engine stores both in one table.
type ClassConstant struct {
	Name string
	// DeclaringClass is where the constant was written. The engine flattens
	// inherited constants into every subclass's table, so a constant reached
	// through a subclass still has to be evaluated with `self` meaning the
	// class whose body declared it.
	DeclaringClass string
	Visibility     string
	Final          bool
	// Case marks an enum case rather than an ordinary constant. Its value is an
	// instance of the enum, built by the runtime rather than harvested.
	Case bool
	// Value is meaningful only when HasValue is set. An initializer referring
	// to another constant is left as an AST by the compiler and evaluated on
	// first use, so there is nothing to record for it here.
	Value    Literal
	HasValue bool
	// Type is the PHP 8.3 declared type, empty when the constant has none.
	Type       string
	Attributes []Attribute
	// Deprecated records #[\Deprecated] and its two optional arguments.
	Deprecated        bool
	DeprecatedMessage *Literal
	DeprecatedSince   *Literal
	// DocComment is the `/** ... */` written before the constant.
	DocComment string
}

// Constant is a constant the engine registers at startup, from Core or from an
// extension.
type Constant struct {
	Name  string
	Value Literal
	// Deprecated is the engine's own flag: using such a constant reports.
	Deprecated bool
}

// Unit is everything one source file compiles to: the file's own op_array plus
// the functions and classes bound into the engine while compiling it.
type Unit struct {
	File *OpArray
	// Diagnostic is whatever the compiler printed while compiling this file and
	// still succeeding -- a deprecation for `${expr}` interpolation, say. It is
	// part of the program's output and appears before anything the program
	// itself prints, so it cannot be discarded just because the compile worked.
	Diagnostic string
	// Functions declared unconditionally at top level. Conditional declarations
	// emit a DECLARE_FUNCTION opcode instead and are only bound at run time, so
	// they are not visible here.
	Functions []*OpArray
	Classes   []*Class
	// AutoGlobals is the file op_array's list: the superglobals the engine
	// creates only for a script that names them.
	AutoGlobals []string
	// LinkFatal is what the engine printed when declaring the file's classes
	// ended in a fatal error, and LinkFatalOpline the ZEND_DECLARE_CLASS
	// that did it; empty when every declaration links.
	LinkFatal       string
	LinkFatalOpline int
	// LinkFatalUnless names a class whose absence alone made the link fail:
	// the fatal holds at run time only if the class is still not there.
	LinkFatalUnless string
}

// Engine is a live PHP interpreter. Zend keeps its globals in unlocked
// process-wide statics, so an Engine owns one locked OS thread and must not be
// used concurrently. Parallelism comes from running several processes.
type Engine struct {
	thread int
	// executed records that PHP has actually run in this engine, which is what
	// makes the more expensive parts of Reset worth doing. Harvesting alone
	// only compiles, and leaves nothing behind to clear.
	executed bool
	// defaultErrorReporting is the mask the build starts with. It is captured
	// before any script runs, because a script that calls error_reporting()
	// overwrites it -- and Reset puts it back.
	defaultErrorReporting int64
}

var active *Engine

// Start boots the embed SAPI. Only one Engine may exist at a time.
func Start() (*Engine, error) {
	if active != nil {
		return nil, errors.New("zend: engine already started")
	}
	// Pin before entering PHP: every subsequent libphp call has to land on this
	// same thread, and Go is otherwise free to migrate the goroutine.
	runtime.LockOSThread()
	if C.php_embed_init(0, nil) == C.FAILURE {
		runtime.UnlockOSThread()
		return nil, errors.New("zend: php_embed_init failed")
	}
	C.php2go_begin_execution()
	active = &Engine{defaultErrorReporting: int64(C.php2go_error_reporting())}
	return active, nil
}

// DefaultErrorReporting is what error_reporting() answers before a script
// changes it. It is an ini default rather than E_ALL.
func (e *Engine) DefaultErrorReporting() int64 { return e.defaultErrorReporting }

// Assertions reports whether this build has assert() enabled, which decides
// which way ZEND_ASSERT_CHECK branches.
func (e *Engine) Assertions() int64 { return int64(C.php2go_assertions()) }

// Close shuts the interpreter down and releases the thread.
func (e *Engine) Close() {
	if active != e {
		return
	}
	C.php2go_stop_record_evals()
	C.php_embed_shutdown()
	active = nil
	runtime.UnlockOSThread()
}

// CompileFile compiles a script without executing it. Nothing in the file runs,
// so harvesting a project has no side effects.
// shebangFlag turns the primary-script flag into the C int the shim takes.
func shebangFlag(primary bool) int {
	if primary {
		return 1
	}
	return 0
}

// CompileFile compiles a file that was included. CompileMainFile is the same
// for the one the request started from.
// armedJITGlobals reads back which just-in-time superglobals the compile that
// just ran created; see php2go_rearm_jit_globals.
func armedJITGlobals() []string {
	var buf [64]C.char
	C.php2go_armed_jit_globals(&buf[0], C.size_t(len(buf)))
	return strings.Fields(C.GoString(&buf[0]))
}

func (e *Engine) CompileFile(path string) (*OpArray, error) {
	return e.compileFile(path, false)
}

// CompileMainFile compiles the script the request started from, which is the
// only one whose `#!` first line is skipped.
func (e *Engine) CompileMainFile(path string) (*OpArray, error) {
	return e.compileFile(path, true)
}

func (e *Engine) compileFile(path string, primary bool) (*OpArray, error) {
	converted, oa, err := e.compileFileKeep(path, primary)
	if oa != nil {
		C.php2go_free_op_array(oa)
	}
	return converted, err
}

// compileFileKeep is compileFile handing the engine's op_array back too, for
// a caller that has more to ask of it; the caller frees it.
func (e *Engine) compileFileKeep(path string, primary bool) (*OpArray, *C.zend_op_array, error) {
	if active != e {
		return nil, nil, errors.New("zend: engine is closed")
	}
	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))

	var bailed C.int
	var diagnostic *C.zend_string
	oa := C.php2go_compile_file(cpath, C.int(shebangFlag(primary)), &bailed, &diagnostic)
	message := ""
	if diagnostic != nil {
		message = goString(diagnostic)
		C.php2go_release(diagnostic)
	}
	if oa == nil {
		// A compile error is a legitimate outcome, not a harness failure: many
		// PHP tests exist precisely to trigger one, and the message is their
		// expected output.
		return nil, nil, &CompileError{Path: path, Diagnostic: message, Bailed: bailed != 0}
	}

	// A successful compile can still print: `${expr}` in a string is deprecated
	// at compile time, for instance. That text is the program's output too.
	converted := convert(oa)
	converted.Diagnostic = message
	converted.AutoGlobals = armedJITGlobals()
	return converted, oa, nil
}

// CompileUnit compiles a script and also returns the functions it declared.
// Nothing is executed.
func (e *Engine) CompileUnit(path string) (*Unit, error) {
	return e.compileUnit(path, false)
}

// CompileMainUnit is CompileUnit for the script the request started from.
func (e *Engine) CompileMainUnit(path string) (*Unit, error) {
	return e.compileUnit(path, true)
}

// CompileEvalString compiles eval's argument the way the engine does, which is
// not the way it compiles a file. `name` is what __FILE__ and diagnostics
// report.
func (e *Engine) CompileEvalString(code, name string) (*Unit, error) {
	if active != e {
		return nil, errors.New("zend: engine is closed")
	}
	ccode, cname := C.CString(code), C.CString(name)
	defer C.free(unsafe.Pointer(ccode))
	defer C.free(unsafe.Pointer(cname))

	var bailed C.int
	var diagnostic, thrown *C.zend_string
	oa := C.php2go_compile_eval(ccode, cname, &bailed, &diagnostic, &thrown)
	message := ""
	if diagnostic != nil {
		message = goString(diagnostic)
		C.php2go_release(diagnostic)
	}
	class := ""
	if thrown != nil {
		class = goString(thrown)
		C.php2go_release(thrown)
	}
	if oa == nil {
		return nil, &CompileError{Path: name, Diagnostic: message, Bailed: bailed != 0, Thrown: class}
	}
	defer C.php2go_free_op_array(oa)
	file := convert(oa)
	file.Diagnostic = message
	file.AutoGlobals = armedJITGlobals()
	return e.unitFor(file), nil
}

func (e *Engine) compileUnit(path string, primary bool) (*Unit, error) {
	file, oa, err := e.compileFileKeep(path, primary)
	if err != nil {
		return nil, err
	}
	defer C.php2go_free_op_array(oa)
	var link *C.zend_op_array
	if primary {
		link = oa
	}
	return e.unitForLinking(file, link), nil
}

// unitFor collects the functions and classes an already-compiled op_array
// declared, and leaves the engine as it was found.
func (e *Engine) unitFor(file *OpArray) *Unit {
	return e.unitForLinking(file, nil)
}

// missingClassFatal is a link failure that depends on a class-like the file
// names and does not declare -- missing outright, not available to a
// variance check, or wanted while autoloading -- which at run time an
// autoloader may supply (type_declarations/variance/trait_success) -- or an
// exception escaping the link, such as an initializer naming a constant the
// script defines at run time (`Uncaught Error: Undefined constant`,
// traits/bug74922).
var unavailableClass = regexp.MustCompile(`because class (\S+) is not available`)

var missingClassFatal = regexp.MustCompile(`((Class|Interface|Trait|Enum) "[^"]*" not found|because class \S+ is not available|while autoloading|Uncaught )`)

// trialLink records unit.LinkFatal (see php2go_trial_link) for the primary
// script's op_array, unless a verdict is not the script's to give.
func (e *Engine) trialLink(unit *Unit, link *C.zend_op_array) {
	// **A script that aliases classes is no verdict**: `class_alias('X',
	// 'Y')` run before `class B extends A { public Y $prop; }` makes Y the
	// same class as X, which a link before anything runs cannot know
	// (variance/property_types_early_bind).
	if link == nil || unitCallsClassAlias(unit) {
		return
	}
	var at C.int
	if printed := C.php2go_trial_link(link, &at); printed != nil {
		text := goString(printed)
		C.php2go_release(printed)
		// **A missing parent, interface or trait is no verdict**: at run
		// time an autoloader may supply it, and here none can.
		if !missingClassFatal.MatchString(text) {
			unit.LinkFatal = text
			unit.LinkFatalOpline = int(at)
		} else if m := unavailableClass.FindStringSubmatch(text); m != nil &&
			!strings.Contains(text, "Uncaught ") && !strings.Contains(text, "while autoloading") {
			// **A class the variance check could not load** is the same
			// fatal at run time if it is still not there once the
			// declaration has tried to load it: conditional on that class
			// (variance/class_order_error).
			unit.LinkFatal = text
			unit.LinkFatalOpline = int(at)
			unit.LinkFatalUnless = m[1]
		}
	}
}

// unitCallsClassAlias reports a `class_alias` literal anywhere in the unit.
func unitCallsClassAlias(unit *Unit) bool {
	bodies := append([]*OpArray{unit.File}, unit.Functions...)
	for _, c := range unit.Classes {
		bodies = append(bodies, c.Methods...)
	}
	for _, oa := range bodies {
		if oa == nil {
			continue
		}
		for _, lit := range oa.Literals {
			if lit.Kind == "string" && strings.EqualFold(lit.Str, "class_alias") {
				return true
			}
		}
	}
	return false
}

// unitForLinking is unitFor that, given the primary script's op_array, also
// trial-links its class declarations once they are harvested (LinkFatal).
func (e *Engine) unitForLinking(file *OpArray, link *C.zend_op_array) *Unit {
	unit := &Unit{File: file, Diagnostic: file.Diagnostic, AutoGlobals: file.AutoGlobals}

	// Match on the filename Zend recorded, not the path passed in: the engine
	// resolves and interns it, so the two need not be identical strings.
	cname := C.CString(file.Filename)
	defer C.free(unsafe.Pointer(cname))
	// Leave the engine as it was found, so each file is harvested independently
	// and a name declared in two files does not collide. Runs after conversion,
	// since deleting destroys the op_arrays being read.
	defer C.php2go_forget_functions(cname)
	defer C.php2go_forget_classes(cname)

	unit.Classes = collectClasses(cname)
	// The trial link runs last, once every function is harvested -- it
	// asks them whether the script aliases classes -- and before the
	// deferred forgets undo the file.
	defer e.trialLink(unit, link)

	count := int(C.php2go_collect_functions(cname, nil, 0))
	if count == 0 {
		return unit
	}
	buf := make([]*C.zend_op_array, count)
	got := int(C.php2go_collect_functions(cname, (**C.zend_op_array)(unsafe.Pointer(&buf[0])), C.int(count)))
	if got > count {
		got = count
	}
	for _, fn := range buf[:got] {
		converted := convert(fn)
		converted.Name = goString(C.php2go_function_name(fn))
		unit.Functions = append(unit.Functions, converted)
	}
	// Deliberately unsorted: the function table is insertion-ordered and the
	// order is observable -- get_defined_functions() reports the order the
	// script declared them in.
	return unit
}

// Builtin is an engine-provided function: its declared signature, taken from
// the build being targeted rather than from documentation.
type Builtin struct {
	Name         string
	Args         []ArgInfo
	ReturnType   string
	RequiredArgs uint32
	Variadic     bool
}

// IniEntry is one directive registered by the linked engine. Nil values are
// distinct from empty strings in ini_get_all(), and Extension is empty for
// directives registered by the engine core rather than a module.
type IniEntry struct {
	Name        string
	GlobalValue *string
	LocalValue  *string
	Access      uint32
	Extension   string
}

// Builtins returns every internal function the linked build provides. This is
// the signature database the type lattice is seeded from, and the specification
// any reimplementation has to satisfy.
func (e *Engine) Builtins() ([]*Builtin, error) {
	if active != e {
		return nil, errors.New("zend: engine is closed")
	}
	count := int(C.php2go_collect_builtins(nil, 0))
	if count == 0 {
		return nil, nil
	}
	buf := make([]*C.zend_internal_function, count)
	got := int(C.php2go_collect_builtins((**C.zend_internal_function)(unsafe.Pointer(&buf[0])), C.int(count)))
	if got > count {
		got = count
	}
	builtins := make([]*Builtin, 0, got)
	for _, fn := range buf[:got] {
		builtins = append(builtins, convertBuiltin(fn))
	}
	sort.Slice(builtins, func(i, j int) bool { return builtins[i].Name < builtins[j].Name })
	return builtins, nil
}

// BuiltinNamesInOrder is the engine's function table as it holds it -- the
// order `get_defined_functions()` lists -- which Builtins sorts away.
func (e *Engine) BuiltinNamesInOrder() ([]string, error) {
	if active != e {
		return nil, errors.New("zend: engine is closed")
	}
	count := int(C.php2go_collect_builtins(nil, 0))
	if count == 0 {
		return nil, nil
	}
	buf := make([]*C.zend_internal_function, count)
	got := int(C.php2go_collect_builtins((**C.zend_internal_function)(unsafe.Pointer(&buf[0])), C.int(count)))
	if got > count {
		got = count
	}
	names := make([]string, 0, got)
	for _, fn := range buf[:got] {
		names = append(names, convertBuiltin(fn).Name)
	}
	return names, nil
}

// IniEntries returns the linked engine's registered INI directives in the
// order ini_get_all() exposes them. Values are captured before user code runs,
// making them safe to carry into an engine-free bundle.
func (e *Engine) IniEntries() ([]IniEntry, error) {
	if active != e {
		return nil, errors.New("zend: engine is closed")
	}
	count := int(C.php2go_collect_ini_entries(nil, 0))
	if count == 0 {
		return nil, nil
	}
	entries := make([]*C.zend_ini_entry, count)
	got := int(C.php2go_collect_ini_entries(
		(**C.zend_ini_entry)(unsafe.Pointer(&entries[0])), C.int(count)))
	if got > count {
		got = count
	}
	result := make([]IniEntry, 0, got)
	for _, entry := range entries[:got] {
		converted := IniEntry{
			Name:   goString(C.php2go_ini_name(entry)),
			Access: uint32(C.php2go_ini_access(entry)),
		}
		if extension := C.php2go_ini_extension(entry); extension != nil {
			converted.Extension = C.GoString(extension)
		}
		if C.php2go_ini_has_global_value(entry) != 0 {
			value := goString(C.php2go_ini_global_value(entry))
			converted.GlobalValue = &value
		}
		if C.php2go_ini_has_local_value(entry) != 0 {
			value := goString(C.php2go_ini_local_value(entry))
			converted.LocalValue = &value
		}
		result = append(result, converted)
	}
	return result, nil
}

// Extensions returns the exact, case-sensitive names in the linked module
// registry. ini_get_all() accepts an extension even when it owns no directives,
// so the INI entries alone cannot provide this membership test.
func (e *Engine) Extensions() ([]string, error) {
	if active != e {
		return nil, errors.New("zend: engine is closed")
	}
	count := int(C.php2go_module_count())
	result := make([]string, 0, count)
	for i := 0; i < count; i++ {
		if name := C.php2go_module_name_at(C.int(i)); name != nil {
			result = append(result, C.GoString(name))
		}
	}
	return result, nil
}

// InternalClasses returns the shape of every class the engine itself provides.
// A user class extending Exception needs that hierarchy to resolve, and a catch
// clause needs it to decide whether a thrown object matches.
func (e *Engine) InternalClasses() ([]*Class, error) {
	if active != e {
		return nil, errors.New("zend: engine is closed")
	}
	count := int(C.php2go_collect_internal_classes(nil, 0))
	if count == 0 {
		return nil, nil
	}
	buf := make([]*C.zend_class_entry, count)
	got := int(C.php2go_collect_internal_classes(
		(**C.zend_class_entry)(unsafe.Pointer(&buf[0])), C.int(count)))
	if got > count {
		got = count
	}
	classes := make([]*Class, 0, got)
	for _, ce := range buf[:got] {
		class := convertClass(ce)
		// An internal method is a C function, so there is no op_array to record
		// and nothing useful in an empty one.
		class.Methods = nil
		classes = append(classes, class)
	}
	sort.Slice(classes, func(i, j int) bool { return classes[i].Name < classes[j].Name })
	return classes, nil
}

// InternalClassOrder returns the names of the engine's own classes in the order
// the class table holds them, which is the order it registered them.
//
// Separate from InternalClasses because that one **sorts**, for the determinism
// its consumers want in emitted tables. `get_declared_classes()` reports
// registration order, and sorting is not recoverable from, so the two are
// different questions rather than one with a flag.
//
// Interfaces and traits come back too; deciding which of those a caller wants
// is the caller's, since `get_declared_classes`, `get_declared_interfaces` and
// `get_declared_traits` each take a different subset.
func (e *Engine) InternalClassOrder() ([]Class, error) {
	if active != e {
		return nil, errors.New("zend: engine is closed")
	}
	count := int(C.php2go_collect_internal_classes(nil, 0))
	if count == 0 {
		return nil, nil
	}
	buf := make([]*C.zend_class_entry, count)
	got := int(C.php2go_collect_internal_classes(
		(**C.zend_class_entry)(unsafe.Pointer(&buf[0])), C.int(count)))
	if got > count {
		got = count
	}
	out := make([]Class, 0, got)
	for _, ce := range buf[:got] {
		c := convertClass(ce)
		// EngineMethods comes along because a stack frame names an engine
		// method as the **engine declares it** -- `Fiber->getReturn()` for any
		// spelling of the call -- and `fn->common.function_name` is the only
		// place that spelling exists. The rest of the class is dropped as
		// before; this is names, not shape.
		// The constants too: `ArrayObject::STD_PROP_LIST` is the engine's to
		// answer, and this is where its value is.
		// And the properties' names: whether an engine class declares one
		// decides whether a reflection that cannot find it is a PHP error or
		// a gap in what this output describes.
		// The whole method table and the create-handler flag: a class the
		// program declares at run time is checked against the first, and
		// the second decides which may be made lazy.
		out = append(out, Class{Name: c.Name, Kind: c.Kind, EngineMethods: c.EngineMethods, EngineTable: c.EngineTable,
			CustomCreate: c.CustomCreate, Constants: c.Constants, Properties: c.Properties})
	}
	return out, nil
}

// FramelessFunctions returns the names behind ZEND_FRAMELESS_ICALL_*, indexed
// by the extended_value those opcodes carry. Without this the opcode names no
// function at all.
func (e *Engine) FramelessFunctions() ([]string, error) {
	if active != e {
		return nil, errors.New("zend: engine is closed")
	}
	count := int(C.php2go_frameless_count())
	names := make([]string, count)
	for i := 0; i < count; i++ {
		names[i] = goString(C.php2go_frameless_name(C.int(i)))
	}
	return names, nil
}

// Constants returns every constant registered before user code runs. Values are
// build-specific -- E_ALL and the SORT_* family have changed across releases --
// so they are read from the linked engine rather than transcribed.
func (e *Engine) Constants() ([]Constant, error) {
	if active != e {
		return nil, errors.New("zend: engine is closed")
	}
	count := int(C.php2go_collect_constants(nil, nil, 0))
	if count == 0 {
		return nil, nil
	}
	entries := make([]*C.zend_constant, count)
	names := make([]*C.zend_string, count)
	got := int(C.php2go_collect_constants(
		(**C.zend_constant)(unsafe.Pointer(&entries[0])),
		(**C.zend_string)(unsafe.Pointer(&names[0])), C.int(count)))
	if got > count {
		got = count
	}
	constants := make([]Constant, 0, got)
	for i := 0; i < got; i++ {
		constants = append(constants, Constant{
			Name:       goString(names[i]),
			Value:      convertZval(C.php2go_constant_value(entries[i])),
			Deprecated: C.php2go_constant_deprecated(entries[i]) != 0,
		})
	}
	sort.Slice(constants, func(i, j int) bool { return constants[i].Name < constants[j].Name })
	return constants, nil
}

func convertBuiltin(fn *C.zend_internal_function) *Builtin {
	builtin := &Builtin{
		Name:         goString(C.php2go_builtin_name(fn)),
		RequiredArgs: uint32(C.php2go_builtin_required_args(fn)),
		Variadic:     C.php2go_builtin_is_variadic(fn) != 0,
	}
	if C.php2go_builtin_has_return_type(fn) != 0 {
		if s := C.php2go_builtin_arg_type(fn, C.int(-1)); s != nil {
			builtin.ReturnType = goString(s)
			C.php2go_release(s)
		}
	}
	count := int(C.php2go_builtin_num_args(fn))
	if builtin.Variadic {
		// As with user functions, num_args excludes the variadic parameter.
		count++
	}
	for i := 0; i < count; i++ {
		arg := ArgInfo{
			Name:     C.GoString(C.php2go_builtin_arg_name(fn, C.int(i))),
			ByRef:    C.php2go_builtin_arg_by_ref(fn, C.int(i)) != 0,
			Variadic: C.php2go_builtin_arg_variadic(fn, C.int(i)) != 0,
			Default:  C.GoString(C.php2go_builtin_arg_default(fn, C.int(i))),
		}
		if s := C.php2go_builtin_arg_type(fn, C.int(i)); s != nil {
			arg.Type = goString(s)
			C.php2go_release(s)
		}
		builtin.Args = append(builtin.Args, arg)
	}
	return builtin
}

// Eval runs PHP source in the engine and returns what it printed. Unlike
// CompileFile this does execute, so it is for differential testing rather than
// harvesting.
func (e *Engine) Eval(code string) (string, error) {
	return e.EvalAs(code, "php2go-eval")
}

// EvalAs evaluates code and reports `name` as the file in any diagnostic.
//
// The name matters because it is observable: "Warning: ... in NAME on line N"
// is graded byte-exact. `Eval` keeps the historical "php2go-eval", which most
// of the corpus's expectations were captured against; a caller comparing
// against a *compiled* program passes the real source path instead, since a
// compiled program names its own file and the two must agree.
func (e *Engine) EvalAs(code, name string) (string, error) {
	if active != e {
		return "", errors.New("zend: engine is closed")
	}
	ccode := C.CString(code)
	defer C.free(unsafe.Pointer(ccode))
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))

	e.executed = true
	var bailed C.int
	captured := C.php2go_eval(ccode, cname, &bailed)
	if captured == nil {
		if bailed != 0 {
			return "", errors.New("zend: evaluation bailed out")
		}
		return "", nil
	}
	out := goString(captured)
	C.php2go_release(captured)
	if bailed != 0 {
		return out, errors.New("zend: evaluation bailed out")
	}
	return out, nil
}

// RecordEvals makes the engine write every string it compiles for eval to
// path, as ReadEvalRecords reads them back: the name it gives the code --
// `FILE(LINE) : eval()'d code` -- and the code. The record is what a compiled
// program can know in advance of an eval whose string is computed: the
// strings this run evaluated at each site, and nothing it did not.
func (e *Engine) RecordEvals(path string) error {
	if active != e {
		return errors.New("zend: engine is closed")
	}
	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))
	if C.php2go_record_evals(cpath) != 0 {
		return fmt.Errorf("zend: cannot record evals to %s", path)
	}
	return nil
}

// EvalRecord is one string the engine compiled for eval (RecordEvals).
type EvalRecord struct {
	// Name is what the engine called the code, which its diagnostics and
	// __FILE__ report.
	Name string
	Code string
}

// ReadEvalRecords reads what RecordEvals wrote, in the order compiled. A
// record cut short by the end of the file -- a process killed mid-write --
// is left out.
func ReadEvalRecords(data []byte) []EvalRecord {
	var out []EvalRecord
	next := func() (string, bool) {
		if len(data) < 4 {
			return "", false
		}
		n := int(binary.NativeEndian.Uint32(data))
		if len(data) < 4+n {
			return "", false
		}
		s := string(data[4 : 4+n])
		data = data[4+n:]
		return s, true
	}
	for {
		name, ok := next()
		if !ok {
			return out
		}
		code, ok := next()
		if !ok {
			return out
		}
		out = append(out, EvalRecord{Name: name, Code: code})
	}
}

// Reset removes every user-defined function and class from the engine.
//
// Harvesting many files in one engine otherwise accumulates declarations: a
// compile that fails part way through has already bound some, and nothing
// removes them. Calling Reset between files keeps each one independent.
func (e *Engine) Reset() error {
	if active != e {
		return errors.New("zend: engine is closed")
	}
	C.php2go_reset_user_symbols()
	// A script that called error_reporting() left the mask changed, and the
	// next one must not inherit it -- the interpreter side always starts from
	// the build's default.
	C.php2go_set_error_reporting(C.zend_long(e.defaultErrorReporting))
	if e.executed {
		C.php2go_clear_autoloaders()
	}
	return nil
}

// danglingClassNameCaches reports how many interned class names still cache a
// class entry that has been removed from the class table. See
// php2go_dangling_ce_caches; the tests assert this is zero after harvesting.
func danglingClassNameCaches() int {
	return int(C.php2go_dangling_ce_caches())
}

// Version reports the PHP version of the linked build. IR is only meaningful
// alongside the version that produced it.
func Version() string {
	return C.GoString(C.php2go_php_version())
}

// OpcodeNames returns the opcode table of the linked build, indexed by opcode.
// Unassigned slots are empty.
func OpcodeNames() []string {
	names := make([]string, 256)
	for opcode := 0; opcode < 256; opcode++ {
		if name := C.php2go_opcode_name(C.uint8_t(opcode)); name != nil {
			names[opcode] = C.GoString(name)
		}
	}
	return names
}

func collectClasses(cname *C.char) []*Class {
	count := int(C.php2go_collect_classes(cname, nil, 0))
	if count == 0 {
		return nil
	}
	buf := make([]*C.zend_class_entry, count)
	got := int(C.php2go_collect_classes(cname, (**C.zend_class_entry)(unsafe.Pointer(&buf[0])), C.int(count)))
	if got > count {
		got = count
	}
	classes := make([]*Class, 0, got)
	for _, ce := range buf[:got] {
		classes = append(classes, convertClass(ce))
	}
	// Deliberately unsorted, as with functions: get_declared_classes() and
	// get_declared_interfaces() report declaration order.
	return classes
}

func convertClass(ce *C.zend_class_entry) *Class {
	flags := uint32(C.php2go_ce_flags(ce))
	class := &Class{
		Name:          goString(C.php2go_ce_name(ce)),
		Kind:          classKind(flags),
		Parent:        goString(C.php2go_ce_parent(ce)),
		Abstract:      flags&C.ZEND_ACC_ABSTRACT != 0,
		Final:         flags&C.ZEND_ACC_FINAL != 0,
		HasGet:        C.php2go_ce_has_get(ce) != 0,
		HasSet:        C.php2go_ce_has_set(ce) != 0,
		HasCall:       C.php2go_ce_has_call(ce) != 0,
		HasCallStatic: C.php2go_ce_has_call_static(ce) != 0,
		// Since PHP 8.2 an undeclared property is deprecated unless the class
		// opted back in with #[AllowDynamicProperties], which the engine
		// records as a class flag rather than as an attribute to re-read.
		AllowDynamicProperties: flags&C.ZEND_ACC_ALLOW_DYNAMIC_PROPERTIES != 0,
		NoDynamicProperties:    flags&C.ZEND_ACC_NO_DYNAMIC_PROPERTIES != 0,
		Readonly:               flags&C.ZEND_ACC_READONLY_CLASS != 0,
		NotSerializable:        flags&C.ZEND_ACC_NOT_SERIALIZABLE != 0,
		CustomCreate:           C.php2go_class_custom_create(ce) != 0,
		NoClone:                C.php2go_class_no_clone(ce) != 0,
		Attributes:             convertAttributes(C.php2go_ce_attributes(ce)),
	}
	class.TraitAliases, class.TraitPrecedences = convertTraitRules(ce)
	if class.Kind == "enum" {
		switch C.php2go_ce_enum_backing_type(ce) {
		case C.IS_LONG:
			class.EnumBackingType = "int"
		case C.IS_STRING:
			class.EnumBackingType = "string"
		}
	}
	for i := uint32(0); i < uint32(C.php2go_ce_num_interfaces(ce)); i++ {
		if name := goString(C.php2go_ce_interface(ce, C.uint32_t(i))); name != "" {
			class.Interfaces = append(class.Interfaces, name)
		}
	}
	for i := uint32(0); i < uint32(C.php2go_ce_num_traits(ce)); i++ {
		if name := goString(C.php2go_ce_trait(ce, C.uint32_t(i))); name != "" {
			class.Traits = append(class.Traits, name)
		}
	}
	class.Filename = goString(C.php2go_ce_filename(ce))
	class.LineStart = uint32(C.php2go_ce_line_start(ce))
	class.LineEnd = uint32(C.php2go_ce_line_end(ce))
	class.DocComment = goString(C.php2go_ce_doc_comment(ce))
	class.Properties = convertProperties(ce)
	class.Methods = convertMethods(ce)
	class.EngineMethods = convertEngineMethods(ce)
	class.EngineTable = convertEngineTable(ce)
	class.Constants = convertClassConstants(ce)
	return class
}

func convertClassConstants(ce *C.zend_class_entry) []ClassConstant {
	count := int(C.php2go_class_constant_count(ce))
	if count == 0 {
		return nil
	}
	entries := make([]*C.zend_class_constant, count)
	names := make([]*C.zend_string, count)
	got := int(C.php2go_collect_class_constants(ce,
		(**C.zend_class_constant)(unsafe.Pointer(&entries[0])),
		(**C.zend_string)(unsafe.Pointer(&names[0])), C.int(count)))
	if got > count {
		got = count
	}
	constants := make([]ClassConstant, 0, got)
	for i := 0; i < got; i++ {
		flags := uint32(C.php2go_class_constant_flags(entries[i]))
		constant := ClassConstant{
			Name:           goString(names[i]),
			DeclaringClass: goString(C.php2go_class_constant_declaring_class(entries[i])),
			Visibility:     visibility(flags),
			Final:          flags&C.ZEND_ACC_FINAL != 0,
			DocComment:     goString(C.php2go_class_constant_doc_comment(entries[i])),
		}
		constant.Case = flags&C.ZEND_CLASS_CONST_IS_CASE != 0
		constant.Attributes = convertAttributes(C.php2go_class_constant_attributes(entries[i]))
		if declared := C.php2go_class_constant_type(entries[i]); declared != nil {
			constant.Type = goString(declared)
			C.php2go_release(declared)
		}
		if attributes := C.php2go_class_constant_attributes(entries[i]); C.php2go_attr_deprecated(attributes) != 0 {
			constant.Deprecated = true
			constant.DeprecatedMessage = convertAttributeArg(attributes, "message")
			constant.DeprecatedSince = convertAttributeArg(attributes, "since")
		}
		switch {
		case C.php2go_class_constant_is_ast(entries[i]) == 0:
			constant.Value = convertZval(C.php2go_class_constant_value(entries[i]))
			constant.HasValue = true
		case constant.Case:
			// A backed case carries its declared value; a pure one has none.
			if backing := C.php2go_enum_case_backing_ast(entries[i]); backing != nil {
				if C.php2go_ast_kind(backing) == C.ZEND_AST_ZVAL {
					constant.Value = convertZval(C.php2go_ast_zval(backing))
				} else if expr := convertAST(backing); expr != nil {
					constant.Value = Literal{Kind: "expr", Expr: expr}
				}
				constant.HasValue = true
			}
		default:
			// An initializer the compiler deferred. The shape is harvested so
			// the runtime can evaluate it the way the engine would on first
			// use; an unreadable one stays a hole.
			if value := convertZval(C.php2go_class_constant_value(entries[i])); value.Kind == "expr" {
				constant.Value = value
				constant.HasValue = true
			}
		}
		constants = append(constants, constant)
	}
	return constants
}

func classKind(flags uint32) string {
	switch {
	case flags&C.ZEND_ACC_ENUM != 0:
		return "enum"
	case flags&C.ZEND_ACC_INTERFACE != 0:
		return "interface"
	case flags&C.ZEND_ACC_TRAIT != 0:
		return "trait"
	}
	return "class"
}

func convertProperties(ce *C.zend_class_entry) []Property {
	count := int(C.php2go_collect_properties(ce, nil, 0))
	if count == 0 {
		return nil
	}
	buf := make([]*C.zend_property_info, count)
	got := int(C.php2go_collect_properties(ce, (**C.zend_property_info)(unsafe.Pointer(&buf[0])), C.int(count)))
	if got > count {
		got = count
	}
	props := make([]Property, 0, got)
	offsets := make([]int64, 0, got)
	virtual := make([]int, 0, got)
	lastOffset := int64(-1)
	for _, info := range buf[:got] {
		flags := uint32(C.php2go_prop_flags(info))
		var nameLen C.size_t
		namePtr := C.php2go_prop_name(info, &nameLen)
		prop := Property{
			Name:           C.GoStringN(namePtr, C.int(nameLen)),
			DeclaringClass: goString(C.php2go_prop_declaring_class(info)),
			Visibility:     visibility(flags),
			Static:         flags&C.ZEND_ACC_STATIC != 0,
			Readonly:       flags&C.ZEND_ACC_READONLY != 0,
			Final:          flags&C.ZEND_ACC_FINAL != 0,
		}
		if s := C.php2go_prop_type(info); s != nil {
			prop.Type = goString(s)
			C.php2go_release(s)
		}
		if def := C.php2go_prop_default(ce, info); def != nil {
			if C.php2go_zval_type(def) != C.IS_UNDEF {
				prop.Default = convertZval(def)
				prop.HasDefault = true
			}
		}
		// Where the engine's properties_info holds it, which is declaration
		// order within a class. The sort below is by slot offset and loses it.
		prop.Order = uint32(len(props))
		prop.Virtual = C.php2go_prop_is_virtual(info) != 0
		prop.SetVisibility = C.GoString(C.php2go_prop_set_visibility(info))
		prop.DocComment = goString(C.php2go_prop_doc_comment(info))
		prop.Attributes = convertAttributes(C.php2go_prop_attributes(info))
		if hook := C.php2go_prop_hook(info, C.php2go_prop_hook_get()); hook != nil {
			prop.GetHook = convert(hook)
			prop.GetHook.Name = goString(C.php2go_function_name(hook))
		}
		if hook := C.php2go_prop_hook(info, C.php2go_prop_hook_set()); hook != nil {
			prop.SetHook = convert(hook)
			prop.SetHook.Name = goString(C.php2go_function_name(hook))
		}
		// A virtual property has no slot, so its offset is a sentinel rather
		// than a position. What places it is the harvest order -- which is the
		// engine's properties_info, in declaration order within a class -- so
		// it takes the offset of the last real property before it and sorts
		// immediately after that one.
		if prop.Virtual {
			offsets = append(offsets, lastOffset)
			virtual = append(virtual, 1)
		} else {
			lastOffset = int64(C.php2go_prop_offset(info))
			offsets = append(offsets, lastOffset)
			virtual = append(virtual, 0)
		}
		props = append(props, prop)
	}
	// Declaration order, which var_dump and foreach both expose. Statics live
	// in a separate table, so their offsets are only comparable among
	// themselves; they sort after the instance properties.
	order := make([]int, len(props))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(i, j int) bool {
		a, b := order[i], order[j]
		if props[a].Static != props[b].Static {
			return !props[a].Static
		}
		if offsets[a] != offsets[b] {
			return offsets[a] < offsets[b]
		}
		return virtual[a] < virtual[b]
	})
	sorted := make([]Property, 0, len(props))
	for _, i := range order {
		sorted = append(sorted, props[i])
	}
	return sorted
}

func visibility(flags uint32) string {
	switch {
	case flags&C.ZEND_ACC_PRIVATE != 0:
		return "private"
	case flags&C.ZEND_ACC_PROTECTED != 0:
		return "protected"
	}
	return "public"
}

// Attribute is one `#[Name(args)]`. The arguments are constant expressions the
// engine keeps unevaluated, so they travel as literals the way a property
// default does.
type Attribute struct {
	Name string
	Args []AttributeArg
	Line uint32
}

// AttributeArg is one argument. Name is empty for a positional one.
type AttributeArg struct {
	Name  string
	Value Literal
}

// TraitAlias is one `T::m as [visibility] n` clause. Class is empty when the
// clause did not name a trait, and Alias is empty when it only changed the
// visibility.
type TraitAlias struct {
	Class      string
	Method     string
	Alias      string
	Visibility string
	// Final is `T::m as final`, which makes the method the class composes
	// final without renaming it.
	Final bool
}

// TraitPrecedence is one `T::m insteadof U, V` clause.
type TraitPrecedence struct {
	Class    string
	Method   string
	Excludes []string
}

// convertTraitRules reads the `as` and `insteadof` clauses.
func convertTraitRules(ce *C.zend_class_entry) ([]TraitAlias, []TraitPrecedence) {
	var aliases []TraitAlias
	for i := 0; i < int(C.php2go_trait_alias_count(ce)); i++ {
		aliases = append(aliases, TraitAlias{
			Class:  goString(C.php2go_trait_alias_class(ce, C.int(i))),
			Method: goString(C.php2go_trait_alias_method(ce, C.int(i))),
			Alias:  goString(C.php2go_trait_alias_name(ce, C.int(i))),
		})
		// A clause with no modifiers only renames; one with them also changes
		// what the method is visible as, or makes it final. Only a visibility
		// modifier decides visibility: `as final` alone leaves it alone.
		modifiers := uint32(C.php2go_trait_alias_modifiers(ce, C.int(i)))
		const ppp = C.ZEND_ACC_PUBLIC | C.ZEND_ACC_PROTECTED | C.ZEND_ACC_PRIVATE
		if modifiers&ppp != 0 {
			aliases[len(aliases)-1].Visibility = visibility(modifiers)
		}
		aliases[len(aliases)-1].Final = modifiers&C.ZEND_ACC_FINAL != 0
	}
	var precedences []TraitPrecedence
	for i := 0; i < int(C.php2go_trait_precedence_count(ce)); i++ {
		rule := TraitPrecedence{
			Class:  goString(C.php2go_trait_precedence_class(ce, C.int(i))),
			Method: goString(C.php2go_trait_precedence_method(ce, C.int(i))),
		}
		for j := uint32(0); j < uint32(C.php2go_trait_precedence_excludes(ce, C.int(i))); j++ {
			rule.Excludes = append(rule.Excludes,
				goString(C.php2go_trait_precedence_exclude(ce, C.int(i), C.uint32_t(j))))
		}
		precedences = append(precedences, rule)
	}
	return aliases, precedences
}

// convertAttributes reads a declaration's whole attribute list. The engine
// keeps it as a packed table of variable-length structs, so every field comes
// through a shim.
func convertAttributes(attrs *C.HashTable) []Attribute {
	return convertAttributesAt(attrs, 0)
}

// convertAttributesAt reads the part of a list written at one offset: 0 is the
// declaration's own attributes, and a parameter's are at its position plus one.
// A function's list holds both, so neither can be read without filtering.
func convertAttributesAt(attrs *C.HashTable, offset uint32) []Attribute {
	count := uint32(C.php2go_attr_count(attrs))
	if count == 0 {
		return nil
	}
	list := make([]Attribute, 0, count)
	for i := uint32(0); i < count; i++ {
		attr := C.php2go_attr_at(attrs, C.uint32_t(i))
		if attr == nil || uint32(C.php2go_attr_offset(attr)) != offset {
			continue
		}
		converted := Attribute{
			Name: goString(C.php2go_attr_full_name(attr)),
			Line: uint32(C.php2go_attr_lineno(attr)),
		}
		argc := uint32(C.php2go_attr_argc(attr))
		for j := uint32(0); j < argc; j++ {
			value := C.php2go_attr_arg_value(attr, C.uint32_t(j))
			if value == nil {
				continue
			}
			converted.Args = append(converted.Args, AttributeArg{
				Name:  goString(C.php2go_attr_arg_name(attr, C.uint32_t(j))),
				Value: convertZval(value),
			})
		}
		list = append(list, converted)
	}
	return list
}

// convertAttributeArg reads one #[\Deprecated] argument, which may be a plain
// string or an expression the compiler deferred.
func convertAttributeArg(attributes *C.HashTable, name string) *Literal {
	key := C.CString(name)
	defer C.free(unsafe.Pointer(key))
	value := C.php2go_attr_deprecated_arg(attributes, key)
	if value == nil {
		return nil
	}
	converted := convertZval(value)
	return &converted
}

func convertEngineMethods(ce *C.zend_class_entry) []EngineMethod {
	count := int(C.php2go_collect_engine_methods(ce, nil, 0))
	if count == 0 {
		return nil
	}
	buf := make([]*C.zend_function, count)
	got := int(C.php2go_collect_engine_methods(ce,
		(**C.zend_function)(unsafe.Pointer(&buf[0])), C.int(count)))
	if got > count {
		got = count
	}
	methods := convertEngineFunctions(buf[:got])
	sort.Slice(methods, func(i, j int) bool { return methods[i].Name < methods[j].Name })
	return methods
}

// convertEngineTable is EngineTable: every internal method, in table order.
func convertEngineTable(ce *C.zend_class_entry) []EngineMethod {
	count := int(C.php2go_collect_engine_table(ce, nil, 0))
	if count == 0 {
		return nil
	}
	buf := make([]*C.zend_function, count)
	got := int(C.php2go_collect_engine_table(ce,
		(**C.zend_function)(unsafe.Pointer(&buf[0])), C.int(count)))
	if got > count {
		got = count
	}
	return convertEngineFunctions(buf[:got])
}

func convertEngineFunctions(fns []*C.zend_function) []EngineMethod {
	methods := make([]EngineMethod, 0, len(fns))
	for _, fn := range fns {
		name := goString(C.php2go_engine_method_name(fn))
		if name == "" {
			continue
		}
		method := EngineMethod{
			Scope:           goString(C.php2go_engine_method_scope(fn)),
			Name:            name,
			Visibility:      C.GoString(C.php2go_engine_method_visibility(fn)),
			Static:          C.php2go_engine_method_is_static(fn) != 0,
			Abstract:        C.php2go_engine_method_is_abstract(fn) != 0,
			Final:           C.php2go_engine_method_is_final(fn) != 0,
			RequiredArgs:    uint32(C.php2go_engine_method_required_args(fn)),
			Variadic:        C.php2go_engine_method_is_variadic(fn) != 0,
			TentativeReturn: C.php2go_engine_method_tentative_return(fn) != 0,
		}
		if C.php2go_engine_method_has_return_type(fn) != 0 {
			if str := C.php2go_engine_method_arg_type(fn, C.int(-1)); str != nil {
				method.ReturnType = goString(str)
				C.php2go_release(str)
			}
		}
		args := int(C.php2go_engine_method_num_args(fn))
		if method.Variadic {
			// As everywhere else, num_args excludes the variadic parameter.
			args++
		}
		for i := 0; i < args; i++ {
			arg := ArgInfo{
				Name:     C.GoString(C.php2go_engine_method_arg_name(fn, C.int(i))),
				ByRef:    C.php2go_engine_method_arg_by_ref(fn, C.int(i)) != 0,
				Variadic: C.php2go_engine_method_arg_variadic(fn, C.int(i)) != 0,
			}
			if str := C.php2go_engine_method_arg_type(fn, C.int(i)); str != nil {
				arg.Type = goString(str)
				C.php2go_release(str)
			}
			arg.Default = C.GoString(C.php2go_engine_method_arg_default(fn, C.int(i)))
			method.Args = append(method.Args, arg)
		}
		methods = append(methods, method)
	}
	return methods
}

func convertMethods(ce *C.zend_class_entry) []*OpArray {
	count := int(C.php2go_collect_methods(ce, nil, 0))
	if count == 0 {
		return nil
	}
	buf := make([]*C.zend_op_array, count)
	got := int(C.php2go_collect_methods(ce, (**C.zend_op_array)(unsafe.Pointer(&buf[0])), C.int(count)))
	if got > count {
		got = count
	}
	methods := make([]*OpArray, 0, got)
	for _, oa := range buf[:got] {
		method := convert(oa)
		method.Name = goString(C.php2go_function_name(oa))
		methods = append(methods, method)
	}
	// Deliberately unsorted: the class's function table is insertion-ordered
	// and that order is observable -- get_class_methods and reflection both
	// report methods in the order they were declared.
	return methods
}

func convert(oa *C.zend_op_array) *OpArray {
	result := &OpArray{
		Filename:   goString(oa.filename),
		LineStart:  uint32(oa.line_start),
		LineEnd:    uint32(oa.line_end),
		NumVars:    int(oa.last_var),
		NumTemps:   uint32(oa.T),
		DocComment: goString(oa.doc_comment),
	}
	result.VarNames = convertVarNames(oa)
	result.Literals = convertLiterals(oa)
	result.StaticVars = convertStaticVars(oa)
	result.Args, result.ReturnType = convertArgs(oa)
	result.NumArgs = uint32(oa.num_args)
	result.RequiredArgs = uint32(oa.required_num_args)
	result.TryCatch = convertTryCatch(oa)
	result.LiveRanges = convertLiveRanges(oa)
	result.Closures = convertClosures(oa)
	result.Visibility = C.GoString(C.php2go_fn_visibility(oa))
	result.Static = C.php2go_fn_is_static(oa) != 0
	result.Abstract = C.php2go_fn_is_abstract(oa) != 0
	result.UsesThis = C.php2go_fn_uses_this(oa) != 0
	result.ReturnsRef = C.php2go_fn_returns_ref(oa) != 0
	result.Attributes = convertAttributes(C.php2go_fn_attributes(oa))
	if attributes := C.php2go_fn_attributes(oa); C.php2go_attr_deprecated(attributes) != 0 {
		result.Deprecated = true
		result.DeprecatedMessage = convertAttributeArg(attributes, "message")
		result.DeprecatedSince = convertAttributeArg(attributes, "since")
	}
	result.Final = C.php2go_fn_is_final(oa) != 0
	result.StrictTypes = C.php2go_fn_strict_types(oa) != 0

	count := int(oa.last)
	if count == 0 || oa.opcodes == nil {
		return result
	}
	// The opline array is mapped as a Go slice rather than read one field at a
	// time through cgo: at roughly 50-100ns per crossing, a per-field approach
	// would dominate the run over millions of oplines.
	oplines := unsafe.Slice(oa.opcodes, count)
	result.Oplines = make([]Opline, count)
	for i := range oplines {
		op := &oplines[i]
		result.Oplines[i] = Opline{
			Index:      i,
			Opcode:     uint8(op.opcode),
			OpcodeName: opcodeName(uint8(op.opcode)),
			Op1Type:    uint8(op.op1_type) & operandKindMask,
			Op2Type:    uint8(op.op2_type) & operandKindMask,
			ResultType: uint8(op.result_type) & operandKindMask,
			// A comparison whose result feeds straight into the next branch is
			// fused by the engine and flagged on the result type. That is real
			// information for a consumer, so it is kept rather than masked away.
			SmartBranch: smartBranch(uint8(op.result_type)),
			// Union members still go through C so the layout lives in one place.
			Op1:      normalizeOperand(oa, op, uint8(op.op1_type), 1, i),
			Op2:      normalizeOperand(oa, op, uint8(op.op2_type), 2, i),
			Result:   normalizeOperand(oa, op, uint8(op.result_type), 0, i),
			Extended: normalizeExtended(op, i),
			Line:     uint32(op.lineno),
		}
	}
	normalizeJumpTables(result)
	return result
}

// normalizeJumpTables rewrites the jump tables behind match and switch.
//
// The table is a literal array whose values are byte offsets relative to the
// opline that reads it -- the same encoding as every other jump in the engine,
// but hidden inside a constant where the operand normalization cannot see it.
// Leaving it raw would put addresses in the IR, which is exactly the mistake
// this layer exists to prevent.
func normalizeJumpTables(oa *OpArray) {
	for i := range oa.Oplines {
		line := &oa.Oplines[i]
		switch line.OpcodeName {
		case "ZEND_MATCH", "ZEND_SWITCH_LONG", "ZEND_SWITCH_STRING":
		default:
			continue
		}
		if line.Op2Type != operandConst || int(line.Op2) >= len(oa.Literals) {
			continue
		}
		table := &oa.Literals[line.Op2]
		for j := range table.Entries {
			entry := &table.Entries[j]
			if entry.Value.Kind != "int" {
				continue
			}
			entry.Value.Int = int64(jumpTarget(uint32(entry.Value.Int), line.Index))
		}
	}
}

func convertClosures(oa *C.zend_op_array) []*OpArray {
	count := uint32(C.php2go_num_closures(oa))
	if count == 0 {
		return nil
	}
	closures := make([]*OpArray, 0, count)
	for i := uint32(0); i < count; i++ {
		nested := C.php2go_closure(oa, C.uint32_t(i))
		if nested == nil {
			continue
		}
		converted := convert(nested)
		converted.Name = goString(C.php2go_function_name(nested))
		closures = append(closures, converted)
	}
	return closures
}

func convertVarNames(oa *C.zend_op_array) []string {
	count := int(oa.last_var)
	if count <= 0 || oa.vars == nil {
		return nil
	}
	names := make([]string, count)
	for i := range names {
		names[i] = goString(C.php2go_var_name(oa, C.int(i)))
	}
	return names
}

func convertStaticVars(oa *C.zend_op_array) []StaticVar {
	count := int(C.php2go_static_var_count(oa))
	if count == 0 {
		return nil
	}
	names := make([]*C.zend_string, count)
	values := make([]*C.zval, count)
	got := int(C.php2go_collect_static_vars(oa,
		(**C.zend_string)(unsafe.Pointer(&names[0])),
		(**C.zval)(unsafe.Pointer(&values[0])), C.int(count)))
	if got > count {
		got = count
	}
	vars := make([]StaticVar, 0, got)
	for i := 0; i < got; i++ {
		entry := StaticVar{Name: goString(names[i])}
		if C.php2go_zval_is_undef(values[i]) == 0 {
			entry.Default = convertZval(values[i])
			entry.HasDefault = true
		}
		vars = append(vars, entry)
	}
	return vars
}

func convertLiterals(oa *C.zend_op_array) []Literal {
	count := int(oa.last_literal)
	if count <= 0 || oa.literals == nil {
		return nil
	}
	literals := make([]Literal, count)
	for i := range literals {
		zv := C.php2go_literal(oa, C.int(i))
		literals[i] = convertZval(zv)
		literals[i].ExtraValue = C.php2go_zval_extra_value(zv) != 0
	}
	return literals
}

func convertZval(zv *C.zval) Literal {
	switch C.php2go_zval_type(zv) {
	case C.IS_NULL:
		return Literal{Kind: "null"}
	case C.IS_FALSE:
		return Literal{Kind: "bool"}
	case C.IS_TRUE:
		return Literal{Kind: "bool", Int: 1}
	case C.IS_LONG:
		return Literal{Kind: "int", Int: int64(C.php2go_zval_long(zv))}
	case C.IS_DOUBLE:
		return Literal{Kind: "float", Float: float64(C.php2go_zval_double(zv))}
	case C.IS_STRING:
		return Literal{Kind: "string", Str: goString(C.php2go_zval_str(zv))}
	case C.IS_ARRAY:
		return Literal{Kind: "array", Entries: convertArrayLiteral(zv)}
	case C.IS_CONSTANT_AST:
		if expr := convertAST(C.php2go_zval_ast(zv)); expr != nil {
			return Literal{Kind: "expr", Expr: expr}
		}
	}
	return Literal{Kind: "other"}
}

// convertAST reads an unevaluated initializer's shape. Only the node kinds a
// constant expression can actually contain are recognised; anything else is
// reported as unsupported so the constant stays a hole rather than becoming a
// plausible wrong value.
func convertAST(ast *C.zend_ast) *Expr {
	if ast == nil {
		return nil
	}
	kind := C.uint(C.php2go_ast_kind(ast))
	line := uint32(C.php2go_ast_lineno(ast))
	expr := &Expr{Op: uint32(C.php2go_ast_attr(ast)), Line: line}
	switch kind {
	case C.ZEND_AST_ZVAL:
		// The attr is a name's qualification -- ZEND_NAME_FQ is 0 -- which
		// an export prints back (`\K::C` against `K::C`).
		return &Expr{Kind: "literal", Op: uint32(C.php2go_ast_attr(ast)), Line: line, Value: convertZval(C.php2go_ast_zval(ast))}
	case C.ZEND_AST_CONSTANT:
		return &Expr{Kind: "constant", Line: line,
			Name: goString(C.php2go_zval_str(C.php2go_ast_zval(ast)))}
	case C.ZEND_AST_CONST:
		expr.Kind = "constant"
	case C.ZEND_AST_CLASS_CONST:
		expr.Kind = "class_constant"
	case C.ZEND_AST_CLASS_NAME:
		expr.Kind = "class_name"
	case C.ZEND_AST_CONSTANT_CLASS:
		// `__CLASS__`, and `self::class` where the compiler could not fold it --
		// inside a trait, where the answer depends on which class composed it.
		// It has no children: the scope is the whole of it.
		expr.Kind = "class_scope"
	case C.ZEND_AST_DIM:
		expr.Kind = "dim"
	case C.ZEND_AST_PROP:
		expr.Kind = "prop"
	case C.ZEND_AST_NULLSAFE_PROP:
		expr.Kind = "nullsafe_prop"
	case C.ZEND_AST_ARRAY:
		expr.Kind = "array"
	case C.ZEND_AST_NEW:
		expr.Kind = "new"
	case C.ZEND_AST_ARG_LIST:
		expr.Kind = "args"
	case C.ZEND_AST_ARRAY_ELEM:
		expr.Kind = "element"
	case C.ZEND_AST_UNPACK:
		// `[...$a]` inside a constant expression. The parser puts this in place
		// of an element rather than inside one, so the array evaluator sees it
		// among the elements.
		expr.Kind = "unpack"
	case C.ZEND_AST_BINARY_OP:
		expr.Kind = "binary"
	case C.ZEND_AST_GREATER:
		// The parser has no "greater" opcode: it emits the smaller-than form
		// with the operands the other way round, so these get their own kinds.
		expr.Kind = "greater"
	case C.ZEND_AST_GREATER_EQUAL:
		expr.Kind = "greater_equal"
	case C.ZEND_AST_AND:
		expr.Kind = "and"
	case C.ZEND_AST_OR:
		expr.Kind = "or"
	case C.ZEND_AST_UNARY_OP:
		expr.Kind = "unary"
	case C.ZEND_AST_UNARY_MINUS:
		expr.Kind = "negate"
	case C.ZEND_AST_UNARY_PLUS:
		expr.Kind = "identity"
	case C.ZEND_AST_CONDITIONAL:
		expr.Kind = "conditional"
	case C.ZEND_AST_COALESCE:
		expr.Kind = "coalesce"
	default:
		// The node kind rides along so a corpus run can report which shapes are
		// still missing rather than only that something was.
		return &Expr{Kind: "unsupported", Line: line, Name: fmt.Sprintf("ast:%d", kind)}
	}
	children := int(C.php2go_ast_children(ast))
	for i := 0; i < children; i++ {
		child := C.php2go_ast_child(ast, C.uint32_t(i))
		if child == nil {
			// A hole -- the middle of `$a ?: $b`, or an unkeyed array element.
			expr.Children = append(expr.Children, nil)
			continue
		}
		expr.Children = append(expr.Children, convertAST(child))
	}
	return expr
}

// convertArrayLiteral expands a folded constant array, recursively.
func convertArrayLiteral(zv *C.zval) []LiteralEntry {
	count := int(C.php2go_array_entries(zv, nil, 0))
	if count == 0 {
		return nil
	}
	buf := make([]C.php2go_entry, count)
	got := int(C.php2go_array_entries(zv, (*C.php2go_entry)(unsafe.Pointer(&buf[0])), C.int(count)))
	if got > count {
		got = count
	}
	entries := make([]LiteralEntry, 0, got)
	for i := range buf[:got] {
		entry := LiteralEntry{
			IntKey:    buf[i].is_int_key != 0,
			IntKeyVal: int64(buf[i].int_key),
		}
		if !entry.IntKey {
			entry.StringKey = goString(buf[i].string_key)
		}
		entry.Value = convertZval(buf[i].value)
		entries = append(entries, entry)
	}
	return entries
}

func convertArgs(oa *C.zend_op_array) ([]ArgInfo, string) {
	attributes := C.php2go_fn_attributes(oa)
	if oa.arg_info == nil {
		return nil, ""
	}
	var returnType string
	if C.php2go_has_return_type(oa) != 0 {
		// The return type lives at index -1, ahead of the parameters.
		if s := C.php2go_arg_type(oa, C.int(-1)); s != nil {
			returnType = goString(s)
			C.php2go_release(s)
		}
	}
	count := int(oa.num_args)
	if C.php2go_is_variadic(oa) != 0 {
		// The variadic parameter is real but not counted in num_args.
		count++
	}
	if count <= 0 {
		return nil, returnType
	}
	args := make([]ArgInfo, count)
	for i := range args {
		arg := ArgInfo{
			Name:     goString(C.php2go_arg_name(oa, C.int(i))),
			ByRef:    C.php2go_arg_by_ref(oa, C.int(i)) != 0,
			Variadic: C.php2go_arg_variadic(oa, C.int(i)) != 0,
		}
		if s := C.php2go_arg_type(oa, C.int(i)); s != nil {
			arg.Type = goString(s)
			C.php2go_release(s)
		}
		arg.Attributes = convertAttributesAt(attributes, uint32(i)+1)
		args[i] = arg
	}
	return args, returnType
}

func convertTryCatch(oa *C.zend_op_array) []TryCatchRange {
	count := int(oa.last_try_catch)
	if count <= 0 || oa.try_catch_array == nil {
		return nil
	}
	entries := unsafe.Slice(oa.try_catch_array, count)
	result := make([]TryCatchRange, count)
	for i := range entries {
		result[i] = TryCatchRange{
			Try:        uint32(entries[i].try_op),
			Catch:      uint32(entries[i].catch_op),
			Finally:    uint32(entries[i].finally_op),
			FinallyEnd: uint32(entries[i].finally_end),
		}
	}
	return result
}

func convertLiveRanges(oa *C.zend_op_array) []LiveRange {
	count := int(oa.last_live_range)
	if count <= 0 || oa.live_range == nil {
		return nil
	}
	ranges := unsafe.Slice(oa.live_range, count)
	result := make([]LiveRange, count)
	for i := range ranges {
		result[i] = LiveRange{
			// cgo renames fields that collide with Go keywords. The raw field
			// is a byte offset with the kind in its low bits, so it needs the
			// engine's own conversion rather than the operand one.
			Var:   uint32(C.php2go_live_slot(C.uint32_t(ranges[i]._var))),
			Start: uint32(ranges[i].start),
			End:   uint32(ranges[i].end),
			Kind:  uint32(C.php2go_live_kind(C.uint32_t(ranges[i]._var))),
		}
	}
	return result
}

// normalizeOperand turns a raw operand into an index a consumer can use: a
// literal index for IS_CONST, a slot number for the variable kinds. which is 1
// or 2 for the operands and 0 for the result, which is never IS_CONST.
// operandKindMask isolates the operand kind from the smart-branch flags the
// engine packs into result_type.
const operandKindMask = 0x0F

// operandConst is the engine's IS_CONST, the operand kind whose number is a
// literal index rather than a slot.
const operandConst = 1

func smartBranch(resultType uint8) string {
	switch {
	case resultType&C.IS_SMART_BRANCH_JMPZ != 0:
		return "jmpz"
	case resultType&C.IS_SMART_BRANCH_JMPNZ != 0:
		return "jmpnz"
	}
	return ""
}

func normalizeOperand(oa *C.zend_op_array, op *C.zend_op, rawKind uint8, which, index int) uint32 {
	kind := rawKind & operandKindMask
	// A jump target is an offset regardless of the operand kind recorded.
	if which == 1 && C.php2go_op1_is_jump(op.opcode) != 0 {
		return jumpTarget(uint32(C.php2go_op1(op)), index)
	}
	if which == 2 && C.php2go_op2_is_jump(op.opcode) != 0 {
		return jumpTarget(uint32(C.php2go_op2(op)), index)
	}
	switch kind {
	case C.IS_CONST:
		switch which {
		case 1:
			return uint32(C.php2go_op1_const_index(oa, op))
		case 2:
			return uint32(C.php2go_op2_const_index(oa, op))
		}
		return 0
	case C.IS_CV, C.IS_TMP_VAR, C.IS_VAR:
		var raw C.uint32_t
		switch which {
		case 1:
			raw = C.php2go_op1(op)
		case 2:
			raw = C.php2go_op2(op)
		default:
			raw = C.php2go_result(op)
		}
		return uint32(C.php2go_var_slot(raw))
	}
	// IS_UNUSED carries jump targets and flags, which are already plain values.
	switch which {
	case 1:
		return uint32(C.php2go_op1(op))
	case 2:
		return uint32(C.php2go_op2(op))
	}
	return uint32(C.php2go_result(op))
}

// normalizeExtended converts a jump target held in extended_value into an
// absolute instruction index, leaving other uses of the field alone.
func normalizeExtended(op *C.zend_op, index int) uint32 {
	if C.php2go_ext_is_jump(op.opcode) != 0 {
		return jumpTarget(uint32(op.extended_value), index)
	}
	return uint32(op.extended_value)
}

// jumpTarget converts a byte offset from the opline into an absolute index.
func jumpTarget(raw uint32, index int) uint32 {
	step := int32(C.php2go_sizeof_zend_op())
	if step == 0 {
		return raw
	}
	return uint32(int32(index) + int32(raw)/step)
}

func opcodeName(opcode uint8) string {
	if name := C.zend_get_opcode_name(C.uint8_t(opcode)); name != nil {
		return C.GoString(name)
	}
	return fmt.Sprintf("UNKNOWN_%d", opcode)
}

func goString(s *C.zend_string) string {
	val := C.php2go_zstr_val(s)
	if val == nil {
		return ""
	}
	return C.GoStringN(val, C.int(C.php2go_zstr_len(s)))
}

// OperandLayout reports where an op_array's literals sit relative to its
// opcodes, and the raw operand of one opline. Temporary diagnostic.
func (e *Engine) OperandLayout(path string, oplineIndex int) (litOff, opOff int64, rawOp2 int32, err error) {
	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))
	var bailed C.int
	var diagnostic *C.zend_string
	oa := C.php2go_compile_file(cpath, 0, &bailed, &diagnostic)
	if diagnostic != nil {
		C.php2go_release(diagnostic)
	}
	if oa == nil {
		return 0, 0, 0, errors.New("compile failed")
	}
	defer C.php2go_free_op_array(oa)
	ops := unsafe.Slice(oa.opcodes, int(oa.last))
	return int64(C.php2go_literals_offset(oa)),
		int64(C.php2go_opline_offset(oa, C.int(oplineIndex))),
		int32(C.php2go_raw_op2(&ops[oplineIndex])), nil
}

// OperandTypeName renders a zend_op operand type for display.
func OperandTypeName(t uint8) string {
	switch t {
	case C.IS_UNUSED:
		return "UNUSED"
	case C.IS_CONST:
		return "CONST"
	case C.IS_TMP_VAR:
		return "TMP"
	case C.IS_VAR:
		return "VAR"
	case C.IS_CV:
		return "CV"
	}
	return fmt.Sprintf("T%d", t)
}
