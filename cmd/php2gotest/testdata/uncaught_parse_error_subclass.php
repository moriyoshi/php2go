<?php
// A subclass of ParseError left uncaught is an ordinary uncaught exception.
class Sub extends ParseError {}
throw new Sub("from a subclass");
