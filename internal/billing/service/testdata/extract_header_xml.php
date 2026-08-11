<?php
$path = $argv[1] ?? '';
$d = new DOMDocument();
$d->load($path);
$xp = new DOMXPath($d);
$xp->registerNamespace('cac', 'urn:oasis:names:specification:ubl:schema:xsd:CommonAggregateComponents-2');
$xp->registerNamespace('cbc', 'urn:oasis:names:specification:ubl:schema:xsd:CommonBasicComponents-2');
foreach (['//cac:TaxTotal/cbc:TaxAmount', '//cac:LegalMonetaryTotal/*'] as $q) {
    foreach ($xp->query($q) as $n) {
        echo $n->nodeName . ': ' . $n->textContent . PHP_EOL;
    }
}
