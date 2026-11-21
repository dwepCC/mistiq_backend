<?php
// Verifica un payload contra la MISMA lógica del facturador: JMS + config/serializer + plantillas Greenter.
// Uso: php verify_facturador_dates.php <facturador_dir> <retention|perception> <payload.json>
// Salida: "DESERIALIZE OK" + fechas del XML; "DESERIALIZE FAIL: ..." (exit 2) si el facturador lo rechazaría.
[$_, $root, $kind, $file] = $argv;
require $root . '/vendor/autoload.php';

use JMS\Serializer\SerializerBuilder;
use Greenter\Model\Retention\Retention;
use Greenter\Model\Perception\Perception;
use Greenter\Xml\Builder\RetentionBuilder;
use Greenter\Xml\Builder\PerceptionBuilder;

$serializer = SerializerBuilder::create()
    ->addMetadataDir($root . '/config/serializer', 'Greenter\\Model')
    ->setPropertyNamingStrategy(new JMS\Serializer\Naming\IdenticalPropertyNamingStrategy())
    ->build();   // mismos defaults que jms_serializer.yaml: formato ATOM (Y-m-d\TH:i:sP)

$class = $kind === 'retention' ? Retention::class : Perception::class;
try {
    $doc = $serializer->deserialize(file_get_contents($file), $class, 'json');
} catch (\Throwable $e) {
    echo "DESERIALIZE FAIL: ", $e->getMessage(), "\n";
    exit(2);
}
echo "DESERIALIZE OK\n";
$builder = $kind === 'retention' ? new RetentionBuilder(['cache' => false, 'strict_variables' => true]) : new PerceptionBuilder(['cache' => false, 'strict_variables' => true]);
$xml = $builder->build($doc);
preg_match_all('#<(cbc:IssueDate|cbc:IssueTime|cbc:PaidDate|sac:SUNATRetentionDate|sac:SUNATPerceptionDate|cbc:Date)>([^<]*)<#', $xml, $m, PREG_SET_ORDER);
foreach ($m as $x) {
    echo "  {$x[1]} = {$x[2]}\n";
}
