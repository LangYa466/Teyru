/**
 * Tree-sitter grammar for Teyru.
 *
 * Teyru is a Java-like language whose compiler lives in this repository
 * (Go: `internal/lexer` + `internal/parser`).  Where the two differ, this
 * grammar follows the implementation:
 *
 *   - there are no semicolons: statements end at a line break,
 *   - a basic `for` header separates its three parts with colons,
 *   - try-with-resources separates its resources with line breaks,
 *   - the enum constant section and the member section are separated by a
 *     single `:`,
 *   - a field followed by a `get`/`set` block is a property, with `field` and
 *     `value` as the storage and the setter parameter,
 *   - `var`, `val`, `record`, `sealed`, `permits`, `when`, `yield`, `module`,
 *     `field`, `value`, `get`, `set` and `non-sealed` are contextual words:
 *     the lexer emits them as identifiers and the parser decides from context.
 *
 * Line breaks are *not* tokens here; see README.md for what that costs.
 */

const PREC = {
  LAMBDA: -1,
  ASSIGN: 1,
  TERNARY: 2,
  OR: 3,
  AND: 4,
  BIT_OR: 5,
  BIT_XOR: 6,
  BIT_AND: 7,
  EQUALITY: 8,
  RELATIONAL: 9,
  SHIFT: 10,
  ADDITIVE: 11,
  MULTIPLICATIVE: 12,
  UNARY: 13,
  CAST: 14,
  POSTFIX: 15,
};

// binary operators, loosest first: the precedence of Java, and of the
// `binPrec` table in internal/parser
const BINARY_OPERATORS = [
  ['||', PREC.OR],
  ['&&', PREC.AND],
  ['|', PREC.BIT_OR],
  ['^', PREC.BIT_XOR],
  ['&', PREC.BIT_AND],
  ['==', PREC.EQUALITY],
  ['!=', PREC.EQUALITY],
  ['<', PREC.RELATIONAL],
  ['>', PREC.RELATIONAL],
  ['<=', PREC.RELATIONAL],
  ['>=', PREC.RELATIONAL],
  ['<<', PREC.SHIFT],
  ['>>', PREC.SHIFT],
  ['>>>', PREC.SHIFT],
  ['+', PREC.ADDITIVE],
  ['-', PREC.ADDITIVE],
  ['*', PREC.MULTIPLICATIVE],
  ['/', PREC.MULTIPLICATIVE],
  ['%', PREC.MULTIPLICATIVE],
];

const ASSIGN_OPERATORS = [
  '=',
  '+=',
  '-=',
  '*=',
  '/=',
  '%=',
  '&=',
  '|=',
  '^=',
  '<<=',
  '>>=',
  '>>>=',
];

// reserved words of internal/lexer, plus `sealed` / `non-sealed` / `default`,
// which only ever appear in front of a declaration
const MODIFIERS = [
  'public',
  'protected',
  'private',
  'static',
  'final',
  'abstract',
  'native',
  'synchronized',
  'transient',
  'volatile',
  'strictfp',
  'sealed',
  'non-sealed',
  'default',
];

const PRIMITIVE_TYPES = [
  'boolean',
  'byte',
  'short',
  'char',
  'int',
  'long',
  'float',
  'double',
  'void',
];

// the shape of a method declaration
const methodShape = $ => [
  repeat($.annotation),
  optional($.modifiers),
  optional($.type_parameters),
  field('type', $._type),
  field('name', $.identifier),
  field('parameters', $.formal_parameters),
  repeat(seq('[', ']')),
  optional($.throws_clause),
  optional($.block),
];

module.exports = grammar({
  name: 'teyru',

  word: $ => $.identifier,

  extras: $ => [
    /\s/,
    $.line_comment,
    $.block_comment,
  ],

  conflicts: $ => [
    // `A.B` is one name in a type or annotation, and an access in an
    // expression; `(x)` is a parenthesized expression, a cast target or the
    // parameter list of a lambda.  Keep both readings in each case and let
    // the one that parses win.
    [$.scoped_identifier, $._primary_expression],
    [$.scoped_identifier, $._primary_expression, $.inferred_parameters],
    // `T[] x` (an array type) and `arr[i]` (an array access)
    [$.array_type, $._type],
    // a statement that is an expression may continue on the same line
    // (`f() + 1`) or end there (`f()`); the line break is not a token
    [$.expression_statement, $._expression],
    // `x instanceof T` (a bare type) and `x instanceof T t` (a pattern)
    [$._type, $._reference_type],
    // `x instanceof Type` (a bare type) and `x instanceof Type(...)` (a record
    // pattern) share the same prefix
    [$._reference_type, $.record_pattern],
    // `Point(a, b)` (a component list) and `f(a, b)` (an argument list) are
    // the same tokens; the parser keeps both and the one that reaches the end
    // of the label wins
    [$.pattern, $._primary_expression],
  ],

  rules: {
    // ----------------------------------------------------------------- file

    // `_member` already covers type declarations, so a compact file (JEP 512)
    // that is nothing but statements parses with the same rule
    source_file: $ => seq(
      optional($.package_declaration),
      repeat($.import_declaration),
      repeat($._member),
    ),

    package_declaration: $ => seq(
      'package',
      field('name', $.scoped_identifier),
    ),

    import_declaration: $ => seq(
      'import',
      choice(
        // `import module a.b` and `import module a.b.*` (JEP 511): parsed and
        // ignored, there is no module system at run time
        seq('module', field('name', $.import_name)),
        seq(optional('static'), field('name', $.import_name)),
      ),
    ),

    // a dotted name grows to the right: at a `.` the parser keeps reading
    // parts instead of ending the name
    scoped_identifier: $ => prec.right(seq(
      $.identifier,
      repeat(seq('.', $.identifier)),
    )),

    // the name of an import is its own rule so that `a.b.C` and the `a.b.*`
    // wildcard share one repetition: at a `.` the parser then never has to
    // choose between carrying on with the name and starting the `.*`.
    import_name: $ => seq(
      $.identifier,
      repeat(seq('.', choice($.identifier, '*'))),
    ),

    // ---------------------------------------------------------------- types

    _type: $ => choice(
      $.primitive_type,
      $.generic_type,
      $.array_type,
      $.scoped_identifier,
    ),

    primitive_type: _ => choice(...PRIMITIVE_TYPES),

    // a type that names a class: what may follow `instanceof`, or open a
    // record pattern.  `x instanceof int` is not a thing, and leaving
    // primitives out is what keeps `x instanceof Foo` from swallowing the
    // `int` of the next statement.
    _reference_type: $ => choice(
      $.generic_type,
      $.array_type,
      $.scoped_identifier,
    ),

    // a `<` right after a type name opens its type arguments, never a
    // comparison, so this rule outranks the bare name it extends
    generic_type: $ => prec.right(1, seq(
      field('name', $.scoped_identifier),
      field('type_arguments', $.type_arguments),
    )),

    array_type: $ => prec.right(seq(
      field('element', choice($.primitive_type, $.generic_type, $.scoped_identifier)),
      repeat1(seq('[', ']')),
    )),

    type_arguments: $ => seq(
      '<',
      optional(commaSep1(choice($._type, $.wildcard))),
      '>',
    ),

    wildcard: $ => seq(
      '?',
      optional(choice(
        seq('extends', $._type),
        seq('super', $._type),
      )),
    ),

    type_parameter: $ => seq(
      repeat($.annotation),
      field('name', $.identifier),
      optional(seq('extends', $._type, repeat(seq('&', $._type)))),
    ),

    type_parameters: $ => seq(
      '<',
      commaSep1($.type_parameter),
      '>',
    ),

    // -------------------------------------------------------- declarations

    _type_declaration: $ => choice(
      $.class_declaration,
      $.interface_declaration,
      $.enum_declaration,
      $.record_declaration,
      $.annotation_type_declaration,
    ),

    class_declaration: $ => prec.right(seq(
      repeat($.annotation),
      optional($.modifiers),
      'class',
      field('name', $.identifier),
      optional($.type_parameters),
      optional($.superclass),
      optional($.super_interfaces),
      optional($.permits_clause),
      optional($.class_body),
    )),

    interface_declaration: $ => prec.right(seq(
      repeat($.annotation),
      optional($.modifiers),
      'interface',
      field('name', $.identifier),
      optional($.type_parameters),
      optional($.extends_interfaces),
      optional($.permits_clause),
      optional($.class_body),
    )),

    enum_declaration: $ => prec.right(seq(
      repeat($.annotation),
      optional($.modifiers),
      'enum',
      field('name', $.identifier),
      optional($.super_interfaces),
      optional($.enum_body),
    )),

    record_declaration: $ => prec.right(seq(
      repeat($.annotation),
      optional($.modifiers),
      'record',
      field('name', $.identifier),
      optional($.type_parameters),
      field('parameters', $.formal_parameters),
      optional($.super_interfaces),
      optional($.record_body),
    )),

    annotation_type_declaration: $ => prec.right(seq(
      repeat($.annotation),
      optional($.modifiers),
      '@',
      'interface',
      field('name', $.identifier),
      optional($.annotation_type_body),
    )),

    superclass: $ => seq('extends', field('type', $._type)),

    super_interfaces: $ => seq('implements', field('type', commaSep1($._type))),

    extends_interfaces: $ => seq('extends', field('type', commaSep1($._type))),

    permits_clause: $ => seq('permits', field('type', commaSep1($._type))),

    modifiers: $ => repeat1(choice(...MODIFIERS)),

    class_body: $ => seq('{', repeat($._member), '}'),

    record_body: $ => seq('{', repeat(choice($._member, $.compact_constructor_declaration)), '}'),

    annotation_type_body: $ => seq(
      '{',
      repeat(choice($._member, $.annotation_method_declaration)),
      '}',
    ),

    enum_body: $ => seq(
      '{',
      optional($.enum_constant_list),
      optional(':'),
      repeat($._member),
      '}',
    ),

    enum_constant_list: $ => prec.right(seq(
      $.enum_constant,
      repeat(seq(',', $.enum_constant)),
      optional(','),
    )),

    // `A(...)`: inside an enum body this is a constant, not a constructor
    enum_constant: $ => prec.right(1, seq(
      repeat($.annotation),
      field('name', $.identifier),
      optional(field('arguments', $.argument_list)),
      optional($.class_body),
    )),

    _member: $ => choice(
      $._type_declaration,
      $.field_declaration,
      // a property: the `{ get ... set ... }` that follows a field
      // declaration.  It is a member of its own so that a field declaration
      // is never left guessing whether a `{` starts an accessor block or an
      // instance initializer; see the conflict below.
      $.accessor_block,
      $.method_declaration,
      $.constructor_declaration,
      $.static_initializer,
      $.instance_initializer,
    ),

    static_initializer: $ => seq('static', $.block),

    instance_initializer: $ => $.block,

    field_declaration: $ => seq(
      repeat($.annotation),
      optional($.modifiers),
      field('type', $._type),
      commaSep1(field('declarator', $.variable_declarator)),
    ),

    variable_declarator: $ => seq(
      field('name', $.identifier),
      repeat(seq('[', ']')),
      optional(seq('=', field('value', choice($.array_initializer, $._expression)))),
    ),

    accessor_block: $ => seq('{', repeat1($.accessor), '}'),

    accessor: $ => seq(
      repeat($.annotation),
      optional($.modifiers),
      choice(
        seq('get', optional($.block)),
        seq('set', optional(field('parameter', $.setter_parameter)), optional($.block)),
      ),
    ),

    setter_parameter: $ => seq('(', field('name', $.identifier), ')'),

    method_declaration: $ => prec.right(seq(...methodShape($))),

    // an element of an `@interface` body that carries a `default` value: the
    // same shape as a method, plus the clause.  `default` only exists there
    // (`parseMemberAfterMods` checks the enclosing declaration kind first), so
    // keeping the clause out of `method_declaration` is what makes
    // `default String kind() { ... }` in an ordinary interface read as a
    // method rather than as the tail of the one before it
    annotation_method_declaration: $ => prec.right(seq(
      repeat($.annotation),
      optional($.modifiers),
      optional($.type_parameters),
      field('type', $._type),
      field('name', $.identifier),
      field('parameters', $.formal_parameters),
      repeat(seq('[', ']')),
      optional($.throws_clause),
      'default',
      field('value', $._element_value),
      optional($.block),
    )),

    // the body is optional: `native` constructors have none, and whether the
    // name really is the class name is a semantic check, not a syntactic one
    // the body is optional: `native` constructors have none, and whether the
    // name really is the class name is a semantic check, not a syntactic one
    constructor_declaration: $ => prec.right(seq(
      repeat($.annotation),
      optional($.modifiers),
      optional($.type_parameters),
      field('name', $.identifier),
      field('parameters', $.formal_parameters),
      optional($.throws_clause),
      optional($.block),
    )),

    // `record Point(int x, int y) { public Point { ... } }`
    compact_constructor_declaration: $ => seq(
      repeat($.annotation),
      optional($.modifiers),
      field('name', $.identifier),
      $.block,
    ),

    throws_clause: $ => seq('throws', commaSep1($._type)),

    formal_parameters: $ => seq('(', optional(commaSep1($.formal_parameter)), ')'),

    formal_parameter: $ => seq(
      repeat($.annotation),
      optional($.modifiers),
      field('type', $._type),
      optional('...'),
      field('name', choice($.identifier, 'this')),
      repeat(seq('[', ']')),
    ),

    // ---------------------------------------------------------- statements

    block: $ => seq('{', repeat($._statement), '}'),

    _statement: $ => choice(
      $.block,
      $.local_variable_declaration,
      $.expression_statement,
      $.if_statement,
      $.while_statement,
      $.do_statement,
      $.for_statement,
      $.enhanced_for_statement,
      $.return_statement,
      $.break_statement,
      $.continue_statement,
      $.throw_statement,
      $.assert_statement,
      $.synchronized_statement,
      $.try_statement,
      $._type_declaration,
      $.labeled_statement,
    ),

    // `yield` is only legal in a switch case body, which is also the only
    // place the compiler's lookahead check (`yield = x` is an assignment to a
    // variable named `yield`) admits the keyword
    _switch_statement: $ => choice($._statement, $.yield_statement),

    yield_statement: $ => prec.right(seq('yield', field('value', $._expression))),

    // `Type name = value` and `name = value` are the same tokens, so the two
    // readings stay open; the compiler tries the declaration first
    // (`tryLocalVar` runs before `parseExpr`) and the dynamic precedence makes
    // the parse agree with it
    local_variable_declaration: $ => prec.dynamic(1, seq(
      repeat($.annotation),
      optional($.modifiers),
      field('type', $._type),
      commaSep1(field('declarator', $.variable_declarator)),
    )),

    // a statement that is just an expression; `switch` reaches this rule
    // through `_expression`
    expression_statement: $ => choice(
      $.assignment_expression,
      $.lambda_expression,
      $.ternary_expression,
      $.binary_expression,
      $.instanceof_expression,
      $.unary_expression,
      $.update_expression,
      $.cast_expression,
      $.switch_construct,
      $._postfix_expression,
    ),

    if_statement: $ => prec.right(seq(
      'if',
      field('condition', $.parenthesized_expression),
      field('consequence', $._statement),
      optional(seq('else', field('alternative', $._statement))),
    )),

    while_statement: $ => seq(
      'while',
      field('condition', $.parenthesized_expression),
      field('body', $._statement),
    ),

    do_statement: $ => seq(
      'do',
      field('body', $._statement),
      'while',
      field('condition', $.parenthesized_expression),
    ),

    // `for (int i = 0 : i < 10 : i++)`, and the empty `for ( : : )`
    for_statement: $ => seq(
      'for',
      '(',
      optional(choice($.local_variable_declaration, commaSep1($._expression))),
      ':',
      optional($._expression),
      ':',
      optional(commaSep1($._expression)),
      ')',
      field('body', $._statement),
    ),

    // `for (T x : xs)`: the `:` right after the name is what tells this apart
    // from a basic `for` whose initialiser is a bare declaration
    enhanced_for_statement: $ => prec.right(1, seq(
      'for',
      '(',
      field('type', $._type),
      field('name', $.identifier),
      ':',
      field('value', $._expression),
      ')',
      field('body', $._statement),
    )),

    switch_construct: $ => seq(
      'switch',
      field('condition', $.parenthesized_expression),
      field('body', $.switch_block),
    ),

    switch_block: $ => seq('{', repeat($.switch_rule), '}'),

    // a `:` case body is a run of statements that stops at the next label;
    // `default` is the only token that could begin either
    switch_rule: $ => prec.left(seq(
      $.switch_label,
      choice(
        // `case 1 -> expr`, `case 1 -> { ... }`, `case 1 -> throw ...`
        seq('->', choice($.block_that_may_yield, $.throw_statement, $._expression)),
        // `case 1:` falls through: statements until the next label
        seq(':', repeat($._switch_statement)),
      ),
    )),

    // an arrow case body: a block whose statements may include `yield`
    block_that_may_yield: $ => seq('{', repeat($._switch_statement), '}'),

    switch_label: $ => choice(
      'default',
      seq('case', commaSep1(choice($.switch_case_label, 'default')), optional($.guard)),
    ),

    guard: $ => seq('when', field('condition', $._expression)),

    // `case null` is an ordinary expression label
    switch_case_label: $ => choice(
      $.type_pattern,
      $.record_pattern,
      $._expression,
    ),

    type_pattern: $ => seq(
      optional('final'),
      field('type', $._type),
      field('name', $.identifier),
    ),

    // `Type(...)` in a case label.  The type is a plain name so that the `(`
    // can be shifted without first reducing a scoped name: that is what keeps
    // this reading alive in the GLR fork with `call(...)`.  The body is its
    // own rule because the conflict above names it.
    record_pattern: $ => prec.dynamic(1, seq(
      field('type', choice($.generic_type, $.scoped_identifier, $.identifier)),
      field('body', $.record_pattern_body),
    )),

    // a record with no components still matches: `case Dot()`
    // the static precedence beats `argument_list` (which is `prec(1)`) where an
    // empty `()` could reduce either way: without it `case Dot()` reads as a
    // call and only a body with components reads as a pattern
    record_pattern_body: $ => prec(2, prec.dynamic(1, seq('(', optional(commaSep1($.pattern)), ')'))),

    pattern: $ => choice(
      $.type_pattern,
      $.record_pattern,
      field('name', $.identifier),
    ),

    return_statement: $ => prec.right(seq('return', optional(field('value', $._expression)))),

    break_statement: $ => prec.right(seq('break', optional(field('label', $.identifier)))),

    continue_statement: $ => prec.right(seq('continue', optional(field('label', $.identifier)))),

    throw_statement: $ => prec.right(seq('throw', field('value', $._expression))),

    assert_statement: $ => prec.right(seq(
      'assert',
      field('condition', $._expression),
      optional(seq(':', field('message', $._expression))),
    )),

    synchronized_statement: $ => seq(
      'synchronized',
      field('lock', $.parenthesized_expression),
      field('body', $.block),
    ),

    labeled_statement: $ => seq(
      field('label', $.identifier),
      ':',
      field('body', $._statement),
    ),

    try_statement: $ => seq(
      'try',
      optional($.resource_specification),
      field('body', $.block),
      repeat($.catch_clause),
      optional($.finally_clause),
    ),

    // the resources of try-with-resources are separated by line breaks
    resource_specification: $ => seq('(', repeat1(choice($.resource, $._expression)), ')'),

    resource: $ => seq(
      repeat($.annotation),
      optional($.modifiers),
      field('type', $._type),
      field('declarator', $.variable_declarator),
    ),

    catch_clause: $ => seq(
      'catch',
      '(',
      optional($.modifiers),
      field('type', commaSep1($._type, '|')),
      field('name', $.identifier),
      ')',
      field('body', $.block),
    ),

    finally_clause: $ => seq('finally', $.block),

    // --------------------------------------------------------- expressions

    _expression: $ => choice(
      $.lambda_expression,
      $.assignment_expression,
      $.ternary_expression,
      $.binary_expression,
      $.instanceof_expression,
      $.unary_expression,
      $.update_expression,
      $.cast_expression,
      $.switch_construct,
      $._postfix_expression,
    ),

    parenthesized_expression: $ => seq('(', $._expression, ')'),

    lambda_expression: $ => prec(PREC.LAMBDA, seq(
      field('parameters', choice($.identifier, $.inferred_parameters, $.formal_parameters)),
      '->',
      field('body', choice($.block, $._expression)),
    )),

    inferred_parameters: $ => seq('(', commaSep1($.identifier), ')'),

    assignment_expression: $ => prec.right(PREC.ASSIGN, seq(
      field('left', $._lhs),
      field('operator', choice(...ASSIGN_OPERATORS)),
      field('right', $._expression),
    )),

    ternary_expression: $ => prec.right(PREC.TERNARY, seq(
      field('condition', $._expression),
      '?',
      field('consequence', choice($._expression, $.lambda_expression)),
      ':',
      field('alternative', choice($._expression, $.lambda_expression)),
    )),

    binary_expression: $ => choice(...BINARY_OPERATORS.map(([operator, precedence]) =>
      prec.left(precedence, seq(
        field('left', $._expression),
        field('operator', operator),
        field('right', $._expression),
      )))),

    // `x instanceof T`, `x instanceof T t`, `x instanceof T(...)`
    instanceof_expression: $ => prec(PREC.RELATIONAL, seq(
      field('left', $._expression),
      'instanceof',
      choice(
        field('type', $._reference_type),
        $.type_pattern,
        $.record_pattern,
      ),
    )),

    unary_expression: $ => choice(
      ...[['+', PREC.UNARY], ['-', PREC.UNARY], ['!', PREC.UNARY], ['~', PREC.UNARY]].map(
        ([operator, precedence]) => prec.left(precedence, seq(
          field('operator', operator),
          field('operand', $._unary_expression),
        )),
      ),
    ),

    update_expression: $ => choice(
      prec.left(PREC.UNARY, seq(
        field('operator', choice('++', '--')),
        field('operand', $._unary_expression),
      )),
      prec.left(PREC.POSTFIX, seq(
        field('operand', $._postfix_expression),
        field('operator', choice('++', '--')),
      )),
    ),

    // like `tryCast`: after a cast to a *primitive* type the operand is read
    // with no lookahead check, so `(byte) -128` is legal; after a cast to a
    // reference type the operand may not start with `+` or `-`
    cast_expression: $ => prec(PREC.CAST, choice(
      seq(
        '(',
        field('type', $.primitive_type),
        ')',
        field('value', choice($._unary_expression, $.lambda_expression)),
      ),
      seq(
        '(',
        field('type', $._reference_type),
        ')',
        field('value', choice($._unary_expression_not_plus_minus, $.lambda_expression)),
      ),
    )),

    _unary_expression: $ => choice(
      $.unary_expression,
      $.update_expression,
      $.cast_expression,
      $._postfix_expression,
    ),

    // like `tryCast`, the operand of a cast may not start with `+` or `-`
    _unary_expression_not_plus_minus: $ => choice(
      $.update_expression,
      $.cast_expression,
      $._postfix_expression,
    ),

    _postfix_expression: $ => choice(
      $._primary_expression,
      $.field_access,
      $.method_invocation,
      $.array_access,
      $.method_reference,
      $.class_literal,
      $.qualified_this,
      $.qualified_super_call,
      $.inner_class_creation,
    ),

    _primary_expression: $ => choice(
      $.parenthesized_expression,
      $.this_expression,
      $.super_expression,
      $._literal,
      $.identifier,
      $.object_creation_expression,
      $.array_creation_expression,
      $.constructor_invocation,
    ),

    this_expression: _ => 'this',

    super_expression: _ => 'super',

    class_literal: $ => prec(PREC.POSTFIX, seq(
      field('type', $._type),
      '.',
      'class',
    )),

    qualified_this: $ => prec(PREC.POSTFIX, seq(
      field('class', $._type),
      '.',
      'this',
    )),

    qualified_super_call: $ => prec(PREC.POSTFIX, seq(
      field('class', $._type),
      '.',
      'super',
      '.',
      field('name', $.identifier),
      field('arguments', $.argument_list),
    )),

    field_access: $ => prec.right(PREC.POSTFIX, seq(
      field('object', $._postfix_expression),
      '.',
      field('field', $.identifier),
    )),

    method_invocation: $ => prec(PREC.POSTFIX, choice(
      seq(field('name', $.identifier), field('arguments', $.argument_list)),
      seq(
        field('object', $._postfix_expression),
        '.',
        optional(field('type_arguments', $.type_arguments)),
        field('name', $.identifier),
        field('arguments', $.argument_list),
      ),
    )),

    array_access: $ => prec(PREC.POSTFIX, seq(
      field('array', $._postfix_expression),
      '[',
      field('index', $._expression),
      ']',
    )),

    method_reference: $ => prec(PREC.POSTFIX, choice(
      seq(
        field('object', $._postfix_expression),
        '::',
        optional(field('type_arguments', $.type_arguments)),
        field('name', choice($.identifier, 'new')),
      ),
      seq(field('type', $._type), '::', field('name', choice($.identifier, 'new'))),
      seq(field('type', $._type), '[', ']', '::', 'new'),
    )),

    // `outer.new Inner()`
    inner_class_creation: $ => prec.right(PREC.POSTFIX, seq(
      field('object', $._postfix_expression),
      '.',
      'new',
      field('name', $.identifier),
      field('arguments', $.argument_list),
      optional($.class_body),
    )),

    // `this(...)` / `super(...)`: the explicit constructor invocation that
    // opens a constructor body
    constructor_invocation: $ => prec(PREC.UNARY, seq(
      field('name', choice('this', 'super')),
      field('arguments', $.argument_list),
    )),

    object_creation_expression: $ => prec.right(PREC.UNARY, seq(
      'new',
      field('type', choice($.generic_type, $.scoped_identifier)),
      field('arguments', $.argument_list),
      optional($.class_body),
    )),

    // `new int[3][]` keeps growing to the right, so a `[` right after the
    // creation belongs to the creation and not to an array access on it
    array_creation_expression: $ => prec.right(seq(
      'new',
      field('type', choice($.primitive_type, $.generic_type, $.scoped_identifier)),
      repeat1(seq('[', optional(field('size', $._expression)), ']')),
      optional(field('value', $.array_initializer)),
    )),

    array_initializer: $ => seq(
      '{',
      optional(seq(commaSep1($._array_initializer_value), optional(','))),
      '}',
    ),

    _array_initializer_value: $ => choice($.array_initializer, $._expression),

    // `A(...)`: inside an enum body this is the argument list of an enum
    // constant, not the parameter list of a constructor named `A`
    argument_list: $ => prec(1, seq('(', optional(commaSep1($._expression)), ')')),

    _lhs: $ => choice($.identifier, $.field_access, $.array_access),

    // ------------------------------------------------------------ literals

    _literal: $ => choice(
      $.decimal_integer_literal,
      $.hex_integer_literal,
      $.binary_integer_literal,
      $.decimal_floating_point_literal,
      $.character_literal,
      $.string_literal,
      $.text_block,
      $.true,
      $.false,
      $.null_literal,
    ),

    decimal_integer_literal: _ => /[0-9][0-9_]*[lL]?/,
    hex_integer_literal: _ => /0[xX][0-9a-fA-F_]+[lL]?/,
    binary_integer_literal: _ => /0[bB][01_]+[lL]?/,
    decimal_floating_point_literal: _ => choice(
      /[0-9][0-9_]*\.[0-9][0-9_]*([eE][+-]?[0-9][0-9_]*)?[fFdD]?/,
      /[0-9][0-9_]*[eE][+-]?[0-9][0-9_]*[fFdD]?/,
      /[0-9][0-9_]*[fFdD]/,
      // `.5`: internal/lexer starts a number at a digit, or at a `.` followed
      // by one
      /\.[0-9][0-9_]*([eE][+-]?[0-9][0-9_]*)?[fFdD]?/,
    ),
    character_literal: _ => token(seq(
      "'",
      choice(/[^'\\\n\r]/, /\\(['"\\bfnrt0-7s]|u[0-9a-fA-F]{4})/),
      "'",
    )),
    string_literal: _ => token(seq(
      '"',
      repeat(choice(/[^"\\\n\r]/, /\\([^u\n\r]|u[0-9a-fA-F]{4})/)),
      '"',
    )),
    // a text block runs to the first `"""`, so the body may hold single
    // quotes and pairs of quotes but never three in a row
    text_block: _ => token(seq(
      '"""',
      /[ \t]*\r?\n/,
      repeat(choice(/[^"\\]/, /\\[\s\S]/, /"[^"]/, /""[^"]/)),
      '"""',
    )),
    true: _ => 'true',
    false: _ => 'false',
    null_literal: _ => 'null',

    // --------------------------------------------------------- annotations

    annotation: $ => seq(
      '@',
      field('name', $.scoped_identifier),
      optional(field('arguments', $.annotation_argument_list)),
    ),

    annotation_argument_list: $ => seq(
      '(',
      optional(commaSep1($.annotation_argument)),
      ')',
    ),

    annotation_argument: $ => choice(
      $._element_value,
      seq(field('name', $.identifier), '=', field('value', $._element_value)),
    ),

    // `parseAnnoValue`: literals, `{...}` arrays, `-lit`, `Name.class` and a
    // dotted name (an enum constant such as `AccessLevel.NONE`), which the
    // compiler reads as a select chain and so is a field access here
    _element_value: $ => choice(
      $._literal,
      $.annotation,
      $.element_value_array_initializer,
      $.field_access,
      $.class_literal,
      seq('-', choice($.decimal_integer_literal, $.decimal_floating_point_literal)),
    ),

    // the elements are element values again, so a nested annotation is allowed
    // (`@Wraps({@Tag("x")})`) where an ordinary array initializer would need an
    // expression
    element_value_array_initializer: $ => seq(
      '{',
      optional(seq(commaSep1($._element_value), optional(','))),
      '}',
    ),

    // -------------------------------------------------------------- tokens

    identifier: _ => /[a-zA-Z_$\u00A1-\uFFFF][a-zA-Z0-9_$\u00A1-\uFFFF]*/,

    line_comment: _ => token(seq('//', /[^\n\r]*/)),

    block_comment: _ => token(seq('/*', /[^*]*\*+([^/*][^*]*\*+)*/, '/')),
  },
});

function commaSep1(rule, separator = ',') {
  return seq(rule, repeat(seq(separator, rule)));
}
